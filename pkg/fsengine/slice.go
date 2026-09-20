package fsengine

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"cworker/pkg/protocol"
)

var (
	// ErrInvalidLineRange 行号区间格式非法错误
	ErrInvalidLineRange = errors.New("invalid line range format (expected 'start:end', 'start:', or ':end')")
)

// ParseLineRange 解析形如 "100:200", "50:", ":30", "100" 的行号区间契约 (1-indexed)
func ParseLineRange(rangeStr string) (int, int, error) {
	rangeStr = strings.TrimSpace(rangeStr)
	if rangeStr == "" {
		return 0, 0, nil
	}

	parts := strings.Split(rangeStr, ":")
	if len(parts) == 1 {
		// 单行 "100"
		n, err := strconv.Atoi(parts[0])
		if err != nil || n < 1 {
			return 0, 0, ErrInvalidLineRange
		}
		return n, n, nil
	}

	if len(parts) == 2 {
		start := 1
		end := 0 // 0 表示直达末尾

		if parts[0] != "" {
			s, err := strconv.Atoi(parts[0])
			if err != nil || s < 1 {
				return 0, 0, ErrInvalidLineRange
			}
			start = s
		}

		if parts[1] != "" {
			e, err := strconv.Atoi(parts[1])
			if err != nil || e < 1 {
				return 0, 0, ErrInvalidLineRange
			}
			end = e
		}

		if end > 0 && end < start {
			return 0, 0, fmt.Errorf("%w: end line (%d) cannot be smaller than start line (%d)", ErrInvalidLineRange, end, start)
		}

		return start, end, nil
	}

	return 0, 0, ErrInvalidLineRange
}

// SliceFile 高性能物理流式切片引擎 (常数内存 O(1)，支持全部任意大小文件)
// 返回值 truncated 指示是否触发了 1MB 默认截断
func SliceFile(file *os.File, opts protocol.TextSliceOptions, w io.Writer) (bool, error) {
	if file == nil {
		return false, errors.New("file handle cannot be nil")
	}

	stat, err := file.Stat()
	if err != nil {
		return false, err
	}
	size := stat.Size()
	if size == 0 {
		return false, nil
	}

	// 1. 用户显式指定行号区间切片 (如 100:200)
	if opts.LineRange != "" {
		start, end, err := ParseLineRange(opts.LineRange)
		if err != nil {
			return false, err
		}
		return false, streamRange(file, start, end, w)
	}

	// 2. 用户显式指定开头 N 行 (Early Stop 早停)
	if opts.Head > 0 {
		return false, streamHead(file, opts.Head, w)
	}

	// 3. 用户显式指定末尾 N 行 (逆向 Seek 定位)
	if opts.Tail > 0 {
		return false, streamTail(file, opts.Tail, size, w)
	}

	// 4. 用户显式指定全量输出 (--all)
	if opts.All {
		return false, streamAll(file, w)
	}

	// 5. 默认行为：超 1MB 自动截取末尾 100 行
	if size <= protocol.DefaultTextSafetyThresholdBytes {
		return false, streamAll(file, w)
	}

	// 超过 1MB 自动安全保底末尾 100 行
	if err := streamTail(file, protocol.DefaultTextTailLines, size, w); err != nil {
		return false, err
	}
	return true, nil
}

// streamAll 常数内存流式直出全量文件
func streamAll(file *os.File, w io.Writer) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := io.Copy(w, file)
	return err
}

// streamHead 流式读取开头 N 行并在满足后立即早停释放
func streamHead(file *os.File, n int, w io.Writer) error {
	if n <= 0 {
		return nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(file, 32*1024)
	lines := 0

	for {
		line, isPrefix, err := reader.ReadLine()
		if len(line) > 0 {
			if _, wErr := w.Write(line); wErr != nil {
				return wErr
			}
		}

		if !isPrefix {
			// 一行结束，补充换行符并递增计数
			if err == nil || len(line) > 0 {
				if _, wErr := w.Write([]byte("\n")); wErr != nil {
					return wErr
				}
				lines++
				if lines >= n {
					return nil // 毫秒级早停退出！
				}
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// streamRange 流式跳过 startLine 前的内容，仅推流区间行，到达 endLine 立即早停
func streamRange(file *os.File, start, end int, w io.Writer) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(file, 32*1024)
	currentLine := 1

	for {
		line, isPrefix, err := reader.ReadLine()
		shouldOutput := currentLine >= start && (end <= 0 || currentLine <= end)

		if shouldOutput && len(line) > 0 {
			if _, wErr := w.Write(line); wErr != nil {
				return wErr
			}
		}

		if !isPrefix {
			if shouldOutput && (err == nil || len(line) > 0) {
				if _, wErr := w.Write([]byte("\n")); wErr != nil {
					return wErr
				}
			}
			if end > 0 && currentLine >= end {
				return nil // 早停
			}
			currentLine++
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// streamTail 倒序 Seek 查找倒数第 N 行的起始 byte offset，然后 Direct IO 直推
// 彻底解除 2MB 假限制，严格扫描直到凑齐要求的 n 行或到达文件开头
func streamTail(file *os.File, n int, size int64, w io.Writer) error {
	if n <= 0 {
		return nil
	}

	// 极小文件直接全部输出
	if size <= 32*1024 {
		return streamTailSmall(file, n, w)
	}

	const chunkSize = int64(64 * 1024)
	offset := size
	newlinesFound := 0
	var targetOffset int64 = 0

	// 倒序分块扫描，内存严格维持在单个 64KB buffer
	buf := make([]byte, chunkSize)
	for offset > 0 {
		readSize := chunkSize
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(file, buf[:readSize]); err != nil {
			return err
		}

		// 在当前 chunk 中从后向前查找 '\n'
		chunk := buf[:readSize]
		for i := len(chunk) - 1; i >= 0; i-- {
			// 忽略文件最末尾紧邻的单个换行符
			if offset+int64(i) == size-1 && chunk[i] == '\n' {
				continue
			}
			if chunk[i] == '\n' {
				newlinesFound++
				if newlinesFound >= n {
					targetOffset = offset + int64(i) + 1
					break
				}
			}
		}

		if newlinesFound >= n {
			break
		}
	}

	// 准确定位后，以 Direct IO 常数内存流式直出
	if _, err := file.Seek(targetOffset, io.SeekStart); err != nil {
		return err
	}
	_, err := io.Copy(w, file)
	return err
}

// streamTailSmall 针对极小文件的快速回溯
func streamTailSmall(file *os.File, n int, w io.Writer) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	lines := bytes.Split(content, []byte("\n"))
	// 若文件末尾有空行，去除最后一个空元素
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	for _, l := range lines {
		if _, err := w.Write(append(l, '\n')); err != nil {
			return err
		}
	}
	return nil
}
