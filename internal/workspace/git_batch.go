package workspace

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// blobBatch reads committed blobs without spawning one Git process per file.
// Object IDs come from ls-tree; filenames are never interpreted as commands.
type blobBatch struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	closed bool
}

func newBlobBatch(ctx context.Context, root string) (*blobBatch, error) {
	cmd := exec.CommandContext(ctx, "git", "-c", "core.fsmonitor=false", "-C", root, "cat-file", "--batch")
	input, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		input.Close()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		input.Close()
		output.Close()
		return nil, e
	}
	return &blobBatch{cmd: cmd, input: input, output: bufio.NewReader(output)}, nil
}
func (b *blobBatch) read(id string) ([]byte, error) {
	if len(id) != 40 && len(id) != 64 {
		return nil, fmt.Errorf("invalid Git blob ID")
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return nil, fmt.Errorf("invalid Git blob ID")
		}
	}
	if _, e := io.WriteString(b.input, id+"\n"); e != nil {
		return nil, e
	}
	header, e := b.output.ReadString('\n')
	if e != nil {
		return nil, e
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != id || fields[1] != "blob" {
		return nil, fmt.Errorf("unexpected Git batch object header")
	}
	size, e := strconv.ParseInt(fields[2], 10, 64)
	if e != nil || size < 0 || uint64(size) > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("invalid Git blob size")
	}
	data := make([]byte, int(size))
	if _, e = io.ReadFull(b.output, data); e != nil {
		return nil, e
	}
	separator, e := b.output.ReadByte()
	if e != nil {
		return nil, e
	}
	if separator != '\n' {
		return nil, fmt.Errorf("invalid Git batch separator")
	}
	return data, nil
}
func (b *blobBatch) finish() error { b.closed = true; _ = b.input.Close(); return b.cmd.Wait() }
func (b *blobBatch) abort() {
	if !b.closed {
		_ = b.input.Close()
		_ = b.cmd.Process.Kill()
		_ = b.cmd.Wait()
		b.closed = true
	}
}
