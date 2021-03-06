//  Copyright 2018 Google Inc. All Rights Reserved.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package daisy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path"
	"sync"
	"time"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/storage"
)

// Logger is a helper that encapsulates the logging logic for Daisy.
type Logger interface {
	StepInfo(w *Workflow, stepName string, format string, a ...interface{})
	WorkflowInfo(w *Workflow, format string, a ...interface{})
	FlushAll()
}

// Log wraps the different logging mechanisms that can be used.
type Log struct {
	gcsLogWriter  *syncedWriter
	cloudLogger   *logging.Logger
	stdoutLogging bool
}

// CreateLogger builds a Logger.
func CreateLogger(ctx context.Context, w *Workflow, cloudLogging bool, gcsLogging bool, stdoutLogging bool) *Log {

	logger := &Log{
		stdoutLogging: stdoutLogging,
	}

	if cloudLogging && w.cloudLoggingClient != nil {
		cloudLogName := fmt.Sprintf("daisy-workflow-%s", w.id)
		logger.cloudLogger = w.cloudLoggingClient.Logger(cloudLogName)
		periodicFlush(func() { logger.cloudLogger.Flush() })
	}

	// Use GCS if unable to use Cloud Logging.
	gcsLogging = gcsLogging || w.cloudLoggingClient == nil

	if gcsLogging {
		logger.gcsLogWriter = &syncedWriter{buf: bufio.NewWriter(&gcsLogger{client: w.StorageClient, bucket: w.bucket, object: path.Join(w.logsPath, "daisy.log"), ctx: ctx})}
		periodicFlush(func() { logger.gcsLogWriter.Flush() })
	}

	return logger
}

// StepInfo logs information for the workflow step.
func (l *Log) StepInfo(w *Workflow, stepName string, format string, a ...interface{}) {
	entry := &LogEntry{
		LocalTimestamp: time.Now(),
		WorkflowName:   getAbsoluteName(w),
		StepName:       stepName,
		Message:        fmt.Sprintf(format, a...),
	}
	l.writeLogEntry(entry)
}

// WorkflowInfo logs information for the workflow.
func (l *Log) WorkflowInfo(w *Workflow, format string, a ...interface{}) {
	entry := &LogEntry{
		LocalTimestamp: time.Now(),
		WorkflowName:   getAbsoluteName(w),
		Message:        fmt.Sprintf(format, a...),
	}
	l.writeLogEntry(entry)
}

// FlushAll flushes all loggers.
func (l *Log) FlushAll() {
	if l.gcsLogWriter != nil {
		l.gcsLogWriter.Flush()
	}

	if l.cloudLogger != nil {
		l.cloudLogger.Flush()
	}
}

// LogEntry encapsulates a single log entry.
type LogEntry struct {
	LocalTimestamp time.Time `json:"localTimestamp"`
	WorkflowName   string    `json:"workflow"`
	StepName       string    `json:"step,omitempty"`
	Message        string    `json:"message"`
}

func (l *Log) writeLogEntry(e *LogEntry) {
	if l.cloudLogger != nil {
		l.cloudLogger.Log(logging.Entry{Payload: e})
	}

	if l.gcsLogWriter != nil {
		l.gcsLogWriter.Write([]byte(e.String()))
	}

	if l.stdoutLogging {
		fmt.Print(e)
	}
}

type syncedWriter struct {
	buf *bufio.Writer
	mx  sync.Mutex
}

func (l *syncedWriter) Write(b []byte) (int, error) {
	l.mx.Lock()
	defer l.mx.Unlock()
	return l.buf.Write(b)
}

func (l *syncedWriter) Flush() error {
	l.mx.Lock()
	defer l.mx.Unlock()
	return l.buf.Flush()
}

type gcsLogger struct {
	client         *storage.Client
	bucket, object string
	buf            *bytes.Buffer
	ctx            context.Context
}

func (l *gcsLogger) Write(b []byte) (int, error) {
	if l.buf == nil {
		l.buf = new(bytes.Buffer)
	}
	l.buf.Write(b)
	wc := l.client.Bucket(l.bucket).Object(l.object).NewWriter(l.ctx)
	wc.ContentType = "text/plain"
	n, err := wc.Write(l.buf.Bytes())
	if err != nil {
		return 0, err
	}
	if err := wc.Close(); err != nil {
		return 0, err
	}
	return n, err
}

func periodicFlush(f func()) {
	go func() {
		for {
			time.Sleep(5 * time.Second)
			f()
		}
	}()
}

func getAbsoluteName(w *Workflow) string {
	name := w.Name
	for parent := w.parent; parent != nil; parent = parent.parent {
		name = parent.Name + "." + name
	}
	return name
}

func (e *LogEntry) String() string {
	var msg string
	if e.StepName != "" {
		msg = fmt.Sprintf("%s: %s", e.StepName, e.Message)
	} else {
		msg = e.Message
	}

	timestamp := e.LocalTimestamp.Format("2006/01/02 15:04:05 MST")
	return fmt.Sprintf("[%s]: %s %s\n", e.WorkflowName, timestamp, msg)
}
