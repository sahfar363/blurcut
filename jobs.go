// BlurCut jobs: one uploaded video + its detections/regions/export state.
// Persisted as jobs.json in the data dir (stdlib only, no DB needed for v1).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Job struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	VideoPath        string      `json:"-"`
	ExportPath       string      `json:"-"`
	PreviewPath      string      `json:"-"`
	HasExport        bool        `json:"hasExport"`
	PreviewConverted bool        `json:"previewConverted"` // served file is a converted mp4 preview, not the original
	Duration         float64     `json:"duration"`
	Width            int         `json:"width"`
	Height           int         `json:"height"`
	FPS              float64     `json:"fps"`
	Status           string      `json:"status"` // uploaded, scanning, scanned, exporting, done, error
	Progress         float64     `json:"progress"`
	Message          string      `json:"message"`
	Detections       []Detection `json:"detections"`
	Regions          []Region    `json:"regions"`
	CreatedAt        time.Time   `json:"createdAt"`
}

type JobStore struct {
	mu   sync.Mutex
	dir  string
	jobs map[string]*Job
}

func openJobStore(dataDir string, tools Tools) (*JobStore, error) {
	s := &JobStore{dir: dataDir, jobs: map[string]*Job{}}
	for _, sub := range []string{"uploads", "exports"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	b, err := os.ReadFile(filepath.Join(dataDir, "jobs.json"))
	if err == nil {
		var list []*Job
		if json.Unmarshal(b, &list) == nil {
			for _, j := range list {
				// A scan/export running when the app was closed never
				// finishes — reset it instead of leaving it stuck.
				if j.Status == "scanning" || j.Status == "exporting" {
					j.Status = "uploaded"
					j.Message = "Interrupted — please scan or export again."
					j.Progress = 0
				}
				// Uploads keep their original extension (id.mkv, id.mov…),
				// so find the real file instead of assuming .mp4.
				j.VideoPath = ""
				if matches, _ := filepath.Glob(filepath.Join(dataDir, "uploads", j.ID+".*")); len(matches) > 0 {
					for _, m := range matches {
						if !strings.HasSuffix(m, "_preview.mp4") {
							j.VideoPath = m
							break
						}
					}
				}
				j.ExportPath = filepath.Join(dataDir, "exports", j.ID+"_blurred.mp4")
				j.PreviewPath = filepath.Join(dataDir, "uploads", j.ID+"_preview.mp4")
				if st, err := os.Stat(j.ExportPath); err == nil && st.Size() > 0 {
					// A partial or broken export left over from an older
					// version must not come back as a download link: check
					// it is really a playable video, and delete it if it
					// is not. Without ffmpeg/ffprobe available, fall back
					// to the size check.
					valid := true
					if tools.FFmpeg != "" || tools.FFprobe != "" {
						valid = verifyExport(tools, j.ExportPath) == nil
					}
					if valid {
						j.HasExport = true
					} else {
						_ = os.Remove(j.ExportPath)
					}
				}
				if _, err := os.Stat(j.PreviewPath); err == nil {
					j.PreviewConverted = true
				}
				s.jobs[j.ID] = j
			}
		}
	}
	return s, nil
}

func (s *JobStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked()
}

func (s *JobStore) saveLocked() {
	list := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		list = append(list, j)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.dir, "jobs.json"), b, 0o644)
}

func (s *JobStore) Add(j *Job) {
	s.mu.Lock()
	s.jobs[j.ID] = j
	s.mu.Unlock()
	s.save()
}

func (s *JobStore) Get(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	return j, ok
}

func (s *JobStore) List() []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j)
	}
	// newest first (IDs are time-prefixed)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (s *JobStore) Delete(id string) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if ok {
		delete(s.jobs, id)
	}
	s.mu.Unlock()
	if ok {
		_ = os.Remove(j.VideoPath)
		_ = os.Remove(j.PreviewPath)
		_ = os.Remove(j.ExportPath)
	}
	s.save()
}

func (s *JobStore) SetStatus(j *Job, status, msg string, progress float64) {
	s.mu.Lock()
	j.Status, j.Message, j.Progress = status, msg, progress
	s.mu.Unlock()
	s.save()
}

func newJobID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
