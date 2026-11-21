package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrExecutor = errors.New("feature executor configuration is invalid")

// CommandConfig describes an already supervised external process. The caller
// (static composition) owns the binary, its OS limits and its network policy;
// this adapter only passes a bounded envelope and enforces a bounded reply.
type CommandConfig struct {
	Binary         string
	Args           []string
	MaxOutputBytes int64
}

// Validate rejects shell tricks, relative binaries and unbounded replies.
func (config CommandConfig) Validate() error {
	if config.Binary == "" || !filepath.IsAbs(config.Binary) {
		return fmt.Errorf("%w: absolute binary path is required", ErrExecutor)
	}
	if strings.ContainsAny(config.Binary, "\x00\n\r") {
		return fmt.Errorf("%w: invalid binary path", ErrExecutor)
	}
	for _, arg := range config.Args {
		if strings.ContainsAny(arg, "\x00\n\r") {
			return fmt.Errorf("%w: invalid argument", ErrExecutor)
		}
	}
	if config.MaxOutputBytes <= 0 || config.MaxOutputBytes > 1<<24 {
		return fmt.Errorf("%w: bounded output limit is required", ErrExecutor)
	}
	return nil
}

// CommandExecutor runs one lifecycle operation in a separate process. It is
// fail-closed: malformed config, process failure or malformed reply are errors,
// never a partially successful lifecycle result.
type CommandExecutor struct {
	config CommandConfig
}

func NewCommandExecutor(config CommandConfig) (*CommandExecutor, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &CommandExecutor{config: config}, nil
}

type boundedWriter struct {
	target *bytes.Buffer
	limit  int64
	total  int64
}

func (writer *boundedWriter) Write(data []byte) (int, error) {
	if writer.total+int64(len(data)) > writer.limit {
		return 0, fmt.Errorf("%w: reply exceeds output limit", ErrExecutor)
	}
	written, err := writer.target.Write(data)
	writer.total += int64(written)
	return written, err
}

type executionEnvelope struct {
	Operation string         `json:"operation"`
	Context   FeatureContext `json:"context"`
}

type executionReply struct {
	OK   bool   `json:"ok"`
	Code string `json:"code,omitempty"`
}

func (executor *CommandExecutor) Execute(ctx context.Context, op Operation, featureContext FeatureContext) error {
	envelope, err := json.Marshal(executionEnvelope{Operation: string(op), Context: featureContext})
	if err != nil {
		return fmt.Errorf("%w: envelope", ErrExecutor)
	}
	command := exec.CommandContext(ctx, executor.config.Binary, executor.config.Args...)
	command.Stdin = bytes.NewReader(envelope)
	var output bytes.Buffer
	command.Stdout = &boundedWriter{target: &output, limit: executor.config.MaxOutputBytes + 1}
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("%w: process failed: %v", ErrExecutor, err)
	}
	if int64(output.Len()) > executor.config.MaxOutputBytes {
		return fmt.Errorf("%w: reply exceeds output limit", ErrExecutor)
	}
	var reply executionReply
	if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
		return fmt.Errorf("%w: malformed reply", ErrExecutor)
	}
	if !reply.OK {
		code := reply.Code
		if code == "" {
			code = "unknown"
		}
		return fmt.Errorf("%w: %s", ErrExecutor, code)
	}
	return nil
}
