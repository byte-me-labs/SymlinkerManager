// Package job runs one apply batch in an elevated helper process. The GUI
// writes a job file, launches this executable with --elevated-job, waits,
// then reads the result file. The helper never opens a window and never
// touches the single instance lock.
package job

import (
	"encoding/json"
	"fmt"
	"os"

	"symlinkermanager/internal/apply"
	"symlinkermanager/internal/config"
	"symlinkermanager/internal/scan"
	"symlinkermanager/internal/winlink"
)

// Request is one serialized apply.Request.
type Request struct {
	Index   int  `json:"index"`
	Checked bool `json:"checked"`
	Intent  int  `json:"intent"`
}

// File tells the helper what to do.
type File struct {
	ConfigPath string    `json:"config_path"`
	ResultPath string    `json:"result_path"`
	Requests   []Request `json:"requests"`
}

// Outcome is a serializable apply.Outcome.
type Outcome struct {
	Index   int    `json:"index"`
	Action  string `json:"action"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// Result is what the helper writes back.
type Result struct {
	Outcomes []Outcome `json:"outcomes"`
	Error    string    `json:"error,omitempty"`
}

// WriteFile stores a job file for the helper.
func WriteFile(path string, f File) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("编码提权任务失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入提权任务失败: %w", err)
	}
	return nil
}

// ReadFile loads a job file.
func ReadFile(path string) (File, error) {
	var f File
	data, err := os.ReadFile(path)
	if err != nil {
		return f, fmt.Errorf("读取提权任务失败: %w", err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("解析提权任务失败: %w", err)
	}
	if f.ConfigPath == "" || f.ResultPath == "" {
		return f, fmt.Errorf("提权任务缺少路径")
	}
	return f, nil
}

// WriteResult stores the helper's outcome.
func WriteResult(path string, r Result) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("编码提权结果失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("写入提权结果失败: %w", err)
	}
	return nil
}

// ReadResult loads the helper's outcome.
func ReadResult(path string) (Result, error) {
	var r Result
	data, err := os.ReadFile(path)
	if err != nil {
		return r, fmt.Errorf("读取提权结果失败: %w", err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("解析提权结果失败: %w", err)
	}
	return r, nil
}

// ToApply converts the serialized rows back to apply.Outcome values.
func (r Result) ToApply() []apply.Outcome {
	out := make([]apply.Outcome, 0, len(r.Outcomes))
	for _, o := range r.Outcomes {
		res := apply.Outcome{Index: o.Index, Action: o.Action, Message: o.Message}
		if o.Error != "" {
			res.Err = fmt.Errorf("%s", o.Error)
		}
		out = append(out, res)
	}
	return out
}

type inspector struct{}

func (inspector) Inspect(path string) (scan.TargetInfo, error) { return winlink.Inspect(path) }

type linker struct{}

func (linker) Create(source, target string) error { return winlink.Create(source, target) }
func (linker) Remove(path string) error           { return winlink.Remove(path) }

// Run executes the job in this (elevated) process.
func Run(f File) Result {
	cfg, err := config.Load(f.ConfigPath)
	if err != nil {
		return Result{Error: "读取配置失败: " + err.Error()}
	}
	scanner := scan.New(inspector{})
	reqs := make([]apply.Request, 0, len(f.Requests))
	for _, r := range f.Requests {
		intent := apply.Intent(r.Intent)
		if intent < apply.IntentAuto || intent > apply.IntentCreateSource {
			intent = apply.IntentAuto
		}
		reqs = append(reqs, apply.Request{Index: r.Index, Checked: r.Checked, Intent: intent})
	}

	scanOne := func(i int) scan.Result {
		all := scanner.Scan(cfg)
		if i < 0 || i >= len(all) {
			return scan.Result{Index: i, Status: scan.StatusError, Message: "条目不存在"}
		}
		return all[i]
	}
	outs := apply.Run(cfg, reqs, scanOne, linker{}, apply.Options{})

	res := Result{Outcomes: make([]Outcome, 0, len(outs))}
	for _, o := range outs {
		row := Outcome{Index: o.Index, Action: o.Action, Message: o.Message}
		if o.Err != nil {
			row.Error = o.Err.Error()
		}
		res.Outcomes = append(res.Outcomes, row)
	}
	return res
}
