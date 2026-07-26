package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/hoax/go-stock/internal/model"
)

// PythonKline 通过项目内 Python 脚本调用 BaoStock 或 AKShare。
// 它只参与后台历史补齐，不用于页面查询路径。
type PythonKline struct {
	name       string
	python     string
	scriptPath string
	timeout    time.Duration
}

func NewPythonKline(name, python, scriptPath string) *PythonKline {
	if python == "" {
		python = "python3"
	}
	if scriptPath == "" {
		scriptPath = "python-provider/fetch_kline.py"
	}
	return &PythonKline{name: name, python: python, scriptPath: scriptPath, timeout: 90 * time.Second}
}

func (p *PythonKline) Name() string { return p.name }

type pythonKlineResponse struct {
	Source string        `json:"source"`
	Klines []model.Kline `json:"klines"`
	Error  string        `json:"error"`
}

func (p *PythonKline) DailyKlines(ctx context.Context, symbol, beg, end string) ([]model.Kline, error) {
	requestCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	cmd := exec.CommandContext(requestCtx, p.python, p.scriptPath, p.name, symbol, beg, end)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if requestCtx.Err() != nil {
		return nil, fmt.Errorf("%s kline: %w", p.name, requestCtx.Err())
	}
	var response pythonKlineResponse
	if decodeErr := json.Unmarshal([]byte(stdout.String()), &response); decodeErr == nil {
		if response.Error != "" {
			return nil, fmt.Errorf("%s kline: %s", p.name, response.Error)
		}
		if err != nil {
			return nil, fmt.Errorf("%s kline exited: %w", p.name, err)
		}
	} else {
		if err != nil {
			detail := strings.TrimSpace(stderr.String())
			if detail == "" {
				detail = strings.TrimSpace(stdout.String())
			}
			return nil, fmt.Errorf("%s kline: %s", p.name, detail)
		}
		return nil, fmt.Errorf("%s kline response: %w", p.name, decodeErr)
	}
	for i := range response.Klines {
		response.Klines[i].Symbol = symbol
		if response.Klines[i].AdjFactor <= 0 {
			response.Klines[i].AdjFactor = 1
		}
	}
	return response.Klines, nil
}
