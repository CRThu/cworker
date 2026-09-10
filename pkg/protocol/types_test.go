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
	if nodeDecoded.Name != node.Name || nodeDecoded.Address != node.Address || nodeDecoded.Status != NodeStatusOnline {
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
