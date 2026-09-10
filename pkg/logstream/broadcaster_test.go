package logstream

import (
	"bytes"
	"testing"
	"time"
)

func TestBroadcaster_WriteAndSubscribe(t *testing.T) {
	b := NewBroadcaster()

	// 1. 无订阅者时写入应该安全完成
	n, err := b.Write([]byte("no subscriber"))
	if err != nil || n != 13 {
		t.Fatalf("write without subscriber failed: n=%d, err=%v", n, err)
	}

	// 2. 两个订阅者同时订阅
	ch1, unsub1 := b.Subscribe()
	ch2, unsub2 := b.Subscribe()

	testMsg := []byte("broadcast test message")
	n, err = b.Write(testMsg)
	if err != nil || n != len(testMsg) {
		t.Fatalf("write failed: %v", err)
	}

	// 接收验证
	select {
	case msg := <-ch1:
		if !bytes.Equal(msg, testMsg) {
			t.Fatalf("ch1 received wrong message: %s", string(msg))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ch1 timeout waiting for message")
	}

	select {
	case msg := <-ch2:
		if !bytes.Equal(msg, testMsg) {
			t.Fatalf("ch2 received wrong message: %s", string(msg))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ch2 timeout waiting for message")
	}

	// 3. 取消 ch1
	unsub1()

	// 再次写入，ch1 不应再收到（通道已关闭），ch2 应正常接收
	msg2 := []byte("second message")
	_, _ = b.Write(msg2)

	select {
	case msg, ok := <-ch1:
		if ok {
			t.Fatalf("expected ch1 to be closed, got: %s", string(msg))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ch1 timeout on closed check")
	}

	select {
	case msg := <-ch2:
		if !bytes.Equal(msg, msg2) {
			t.Fatalf("ch2 received wrong message: %s", string(msg))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ch2 timeout waiting for message")
	}

	unsub2()

	// 4. 关闭广播器
	b.Close()
	// 重复关闭应该安全
	b.Close()

	// 关闭后写入应安全返回
	_, _ = b.Write([]byte("after close"))

	// 关闭后订阅应直接返回已关闭通道
	ch3, unsub3 := b.Subscribe()
	unsub3()
	if _, ok := <-ch3; ok {
		t.Fatal("expected ch3 to be closed immediately")
	}
}
