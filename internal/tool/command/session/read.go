package session

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// OutputCursorError 明确区分过期/无效游标；不默默跳过未返回的日志。
type OutputCursorError struct {
	Stream                     string
	Requested, Retained, Total int64
}

func (e *OutputCursorError) Error() string {
	return fmt.Sprintf("%s offset %d is outside retained range [%d,%d]", e.Stream, e.Requested, e.Retained, e.Total)
}

func segment(data []byte, base int64, requested *int64, budget int, stream string) (string, int64, int64, int, string, error) {
	offset := base
	if requested != nil {
		offset = *requested
	}
	total := base + int64(len(data))
	if offset < base || offset > total {
		return "", offset, offset, 0, "", &OutputCursorError{stream, offset, base, total}
	}
	raw := data[int(offset-base):]
	n := min(len(raw), max(0, budget))
	encoding := "utf-8"
	value := string(raw[:n])
	if !utf8.ValidString(value) {
		encoding = "utf-8-lossy"
		value = strings.ToValidUTF8(value, "�")
	}
	return value, offset, offset + int64(n), len(raw) - n, encoding, nil
}

// SnapshotAt 的游标由每个调用者独立维护，重复请求不改变任何共享状态。
// maxBytes 是两条流共同的原始字节预算；最终序列化另受线协议总预算约束。
func (s *Session) SnapshotAt(status string, maxBytes int, outOffset, errOffset *int64) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	outBase, errBase := int64(s.stdoutDroppedBytes), int64(s.stderrDroppedBytes)
	outTotal, errTotal := outBase+int64(s.stdout.Len()), errBase+int64(s.stderr.Len())
	outStart, errStart := outBase, errBase
	if outOffset != nil {
		outStart = *outOffset
	}
	if errOffset != nil {
		errStart = *errOffset
	}
	if outStart < outBase || outStart > outTotal {
		return Snapshot{}, &OutputCursorError{"stdout", outStart, outBase, outTotal}
	}
	if errStart < errBase || errStart > errTotal {
		return Snapshot{}, &OutputCursorError{"stderr", errStart, errBase, errTotal}
	}
	outBudget := maxBytes
	if outTotal > outStart && errTotal > errStart {
		outBudget = maxBytes - maxBytes/2
	}
	stdout, oo, on, om, oe, err := segment(s.stdout.Bytes(), outBase, outOffset, outBudget, "stdout")
	if err != nil {
		return Snapshot{}, err
	}
	stderr, eo, en, em, ee, err := segment(s.stderr.Bytes(), errBase, errOffset, maxBytes-int(on-oo), "stderr")
	if err != nil {
		return Snapshot{}, err
	}
	end := time.Now()
	if s.completed {
		end = s.FinishedAt
	}
	snapshot := Snapshot{SessionID: s.ID, Status: status, Stdout: stdout, Stderr: stderr, ElapsedMS: end.Sub(s.StartedAt).Milliseconds(), TimedOut: s.TimedOut, Terminal: s.Terminal,
		StdoutOutputBytes: len(stdout), StderrOutputBytes: len(stderr), StdoutTotalBytes: outTotal, StderrTotalBytes: errTotal,
		StdoutDroppedBytes: outBase, StderrDroppedBytes: errBase, StdoutOmittedBytes: om, StderrOmittedBytes: em,
		StdoutOutputLines: countLines(stdout), StderrOutputLines: countLines(stderr), StdoutTruncated: om > 0, StderrTruncated: em > 0,
		StdoutOffset: oo, StderrOffset: eo, StdoutNextOffset: on, StderrNextOffset: en, StdoutEncoding: oe, StderrEncoding: ee,
		Completed: s.completed, ExitCode: s.exitCode, CommandOK: s.completed && s.exitCode == 0 && !s.TimedOut,
		Runtime: s.execution.Runtime, WSLDistribution: s.execution.Distribution, Workdir: s.execution.Workdir}
	if oe == "utf-8-lossy" {
		snapshot.StdoutBase64 = base64.StdEncoding.EncodeToString(s.stdout.Bytes()[int(oo-outBase):int(on-outBase)])
	}
	if ee == "utf-8-lossy" {
		snapshot.StderrBase64 = base64.StdEncoding.EncodeToString(s.stderr.Bytes()[int(eo-errBase):int(en-errBase)])
	}
	return snapshot, nil
}
