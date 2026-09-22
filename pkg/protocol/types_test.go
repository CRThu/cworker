package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProtocol_TypesSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	// 1. NodeInfo & NodeMetrics
	node := NodeInfo{
		Name:    "desktop-4090",
		Address: "192.168.1.100:19000",
		Status:  NodeStatusOnline,
		Metrics: NodeMetrics{
			CPUPercent: 12.5,
			CPUCores:   16,
			MemFreeMB:  8192,
			MemTotalMB: 16384,
		},
		ActiveJobs: 2,
		LastSeen:   now,
	}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal NodeInfo failed: %v", err)
	}
	var nodeDecoded NodeInfo
	if err := json.Unmarshal(data, &nodeDecoded); err != nil {
		t.Fatalf("unmarshal NodeInfo failed: %v", err)
	}
	if nodeDecoded.Name != node.Name || nodeDecoded.Address != node.Address || nodeDecoded.Status != NodeStatusOnline || nodeDecoded.Metrics.CPUCores != 16 {
		t.Fatalf("NodeInfo mismatch: %+v vs %+v", node, nodeDecoded)
	}

	// 2. JobInfo & JobMetrics
	endTime := now.Add(5 * time.Second)
	job := JobInfo{
		ID:      "job-test1",
		Name:    "compile",
		Node:    "desktop-4090",
		Command: "go build",
		Dir:     "C:\\project",
		Status:  JobStatusCompleted,
		PID:     1234,
		Metrics: JobMetrics{
			CPUPercent: 25.0,
			MemoryMB:   256,
		},
		StartTime: now,
		EndTime:   &endTime,
		ExitCode:  0,
	}
	data, err = json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal JobInfo failed: %v", err)
	}
	var jobDecoded JobInfo
	if err := json.Unmarshal(data, &jobDecoded); err != nil {
		t.Fatalf("unmarshal JobInfo failed: %v", err)
	}
	if jobDecoded.ID != job.ID || jobDecoded.ExitCode != 0 || jobDecoded.Status != JobStatusCompleted {
		t.Fatalf("JobInfo mismatch: %+v vs %+v", job, jobDecoded)
	}

	// 2.1 JobStatusStopped (测试自愈与主动终止状态序列化)
	stoppedJob := JobInfo{
		ID:       "job-stopped-reboot",
		Status:   JobStatusStopped,
		ExitCode: -1,
	}
	data, err = json.Marshal(stoppedJob)
	if err != nil {
		t.Fatalf("marshal stopped JobInfo failed: %v", err)
	}
	var stDecoded JobInfo
	if err := json.Unmarshal(data, &stDecoded); err != nil {
		t.Fatalf("unmarshal stopped JobInfo failed: %v", err)
	}
	if stDecoded.Status != JobStatusStopped || stDecoded.ExitCode != -1 {
		t.Fatalf("JobStatusStopped mismatch: %+v", stDecoded)
	}

	// 3. Requests
	runReq := RunJobRequest{
		Name:    "train",
		Node:    "desktop-4090",
		Command: "python train.py",
		Dir:     "D:\\ai",
	}
	data, _ = json.Marshal(runReq)
	var runReqDecoded RunJobRequest
	_ = json.Unmarshal(data, &runReqDecoded)
	if runReqDecoded.Command != runReq.Command {
		t.Fatalf("RunJobRequest mismatch")
	}

	killReq := KillJobRequest{JobID: "job-test1"}
	data, _ = json.Marshal(killReq)
	var killReqDecoded KillJobRequest
	_ = json.Unmarshal(data, &killReqDecoded)
	if killReqDecoded.JobID != killReq.JobID {
		t.Fatalf("KillJobRequest mismatch")
	}

	// 4. KnownNode
	known := KnownNode{
		Name:   "node-alpha",
		Target: "192.168.1.50:19000",
		Token:  "secret123",
	}
	data, _ = json.Marshal(known)
	var knownDecoded KnownNode
	_ = json.Unmarshal(data, &knownDecoded)
	if knownDecoded.Name != known.Name || knownDecoded.Token != known.Token {
		t.Fatalf("KnownNode mismatch")
	}

	// 5. FileInfo
	fileInfo := FileInfo{
		Name:    "dataset.csv",
		Path:    "D:\\data\\dataset.csv",
		IsDir:   false,
		Size:    1024000,
		ModTime: now,
	}
	data, _ = json.Marshal(fileInfo)
	var fileDecoded FileInfo
	_ = json.Unmarshal(data, &fileDecoded)
	if fileDecoded.Name != fileInfo.Name || fileDecoded.Size != fileInfo.Size {
		t.Fatalf("FileInfo mismatch")
	}
}

func TestProtocol_NodeMetrics_BackwardCompatibility(t *testing.T) {
	// 1. 旧版本 Worker 返回的 JSON：无 cpu_cores 字段
	legacyJSON := `{"name":"legacy-node","status":"ONLINE","metrics":{"cpu_percent":12.5,"mem_free_mb":4096,"mem_total_mb":8192}}`
	var legacyNode NodeInfo
	if err := json.Unmarshal([]byte(legacyJSON), &legacyNode); err != nil {
		t.Fatalf("unmarshal legacy NodeInfo failed: %v", err)
	}
	if legacyNode.Metrics.CPUCores != 0 {
		t.Fatalf("expected legacy node CPUCores to be 0, got: %d", legacyNode.Metrics.CPUCores)
	}
	if legacyNode.Metrics.CPUPercent != 12.5 {
		t.Fatalf("expected CPUPercent 12.5, got: %f", legacyNode.Metrics.CPUPercent)
	}

	// 2. 新版本 Worker 返回的 JSON：包含 cpu_cores 字段
	modernJSON := `{"name":"modern-node","status":"ONLINE","metrics":{"cpu_percent":15.0,"cpu_cores":32,"mem_free_mb":8192,"mem_total_mb":32768}}`
	var modernNode NodeInfo
	if err := json.Unmarshal([]byte(modernJSON), &modernNode); err != nil {
		t.Fatalf("unmarshal modern NodeInfo failed: %v", err)
	}
	if modernNode.Metrics.CPUCores != 32 {
		t.Fatalf("expected modern node CPUCores to be 32, got: %d", modernNode.Metrics.CPUCores)
	}
}

func TestProtocol_FsHashEventSerialization(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	// 1. Init event
	initEv := FsHashEvent{
		Event:      FsHashEventInit,
		TotalFiles: 120,
		TotalBytes: 104857600,
	}
	data, err := json.Marshal(initEv)
	if err != nil {
		t.Fatalf("marshal init event failed: %v", err)
	}
	var initDecoded FsHashEvent
	if err := json.Unmarshal(data, &initDecoded); err != nil {
		t.Fatalf("unmarshal init event failed: %v", err)
	}
	if initDecoded.Event != FsHashEventInit || initDecoded.TotalFiles != 120 || initDecoded.TotalBytes != 104857600 {
		t.Fatalf("init event mismatch: %+v", initDecoded)
	}

	// 2. Progress event
	progEv := FsHashEvent{
		Event:       FsHashEventProgress,
		CurrentFile: "large.iso",
		DoneBytes:   52428800,
		TotalBytes:  104857600,
	}
	data, err = json.Marshal(progEv)
	if err != nil {
		t.Fatalf("marshal progress event failed: %v", err)
	}
	var progDecoded FsHashEvent
	if err := json.Unmarshal(data, &progDecoded); err != nil {
		t.Fatalf("unmarshal progress event failed: %v", err)
	}
	if progDecoded.Event != FsHashEventProgress || progDecoded.CurrentFile != "large.iso" || progDecoded.DoneBytes != 52428800 {
		t.Fatalf("progress event mismatch: %+v", progDecoded)
	}

	// 3. Entry event
	entryEv := FsHashEvent{
		Event: FsHashEventEntry,
		Entry: &FileInfo{
			Name:    "file.txt",
			Path:    "sub/file.txt",
			Size:    1024,
			ModTime: now,
			SHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}
	data, err = json.Marshal(entryEv)
	if err != nil {
		t.Fatalf("marshal entry event failed: %v", err)
	}
	var entryDecoded FsHashEvent
	if err := json.Unmarshal(data, &entryDecoded); err != nil {
		t.Fatalf("unmarshal entry event failed: %v", err)
	}
	if entryDecoded.Event != FsHashEventEntry || entryDecoded.Entry == nil || entryDecoded.Entry.SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("entry event mismatch: %+v", entryDecoded)
	}

	// 4. Done event
	doneEv := FsHashEvent{
		Event:      FsHashEventDone,
		TotalFiles: 120,
		TotalBytes: 104857600,
	}
	data, err = json.Marshal(doneEv)
	if err != nil {
		t.Fatalf("marshal done event failed: %v", err)
	}
	var doneDecoded FsHashEvent
	if err := json.Unmarshal(data, &doneDecoded); err != nil {
		t.Fatalf("unmarshal done event failed: %v", err)
	}
	if doneDecoded.Event != FsHashEventDone || doneDecoded.TotalFiles != 120 {
		t.Fatalf("done event mismatch: %+v", doneDecoded)
	}
}

func TestProtocol_FsRmEventSerialization(t *testing.T) {
	// 1. Progress event
	progEv := FsRmEvent{
		Event:        FsRmEventProgress,
		RemovedCount: 1520,
	}
	data, err := json.Marshal(progEv)
	if err != nil {
		t.Fatalf("marshal progress event failed: %v", err)
	}
	var progDecoded FsRmEvent
	if err := json.Unmarshal(data, &progDecoded); err != nil {
		t.Fatalf("unmarshal progress event failed: %v", err)
	}
	if progDecoded.Event != FsRmEventProgress || progDecoded.RemovedCount != 1520 {
		t.Fatalf("progress event mismatch: %+v", progDecoded)
	}

	// 2. Done event
	doneEv := FsRmEvent{
		Event:        FsRmEventDone,
		RemovedCount: 8848,
	}
	data, err = json.Marshal(doneEv)
	if err != nil {
		t.Fatalf("marshal done event failed: %v", err)
	}
	var doneDecoded FsRmEvent
	if err := json.Unmarshal(data, &doneDecoded); err != nil {
		t.Fatalf("unmarshal done event failed: %v", err)
	}
	if doneDecoded.Event != FsRmEventDone || doneDecoded.RemovedCount != 8848 {
		t.Fatalf("done event mismatch: %+v", doneDecoded)
	}

	// 3. Error event
	errEv := FsRmEvent{
		Event: FsRmEventError,
		Error: "permission denied",
	}
	data, err = json.Marshal(errEv)
	if err != nil {
		t.Fatalf("marshal error event failed: %v", err)
	}
	var errDecoded FsRmEvent
	if err := json.Unmarshal(data, &errDecoded); err != nil {
		t.Fatalf("unmarshal error event failed: %v", err)
	}
	if errDecoded.Event != FsRmEventError || errDecoded.Error != "permission denied" {
		t.Fatalf("error event mismatch: %+v", errDecoded)
	}
}

