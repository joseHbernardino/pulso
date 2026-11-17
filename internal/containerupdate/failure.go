package containerupdate

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var consoleColor = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Preserve startup errors before the failed candidate is removed during rollback.
func (d *Engine) failure(ctx context.Context, id, message string) error {
	response, err := d.call(ctx, "GET", "/containers/"+id+"/logs?stdout=true&stderr=true&tail=20", nil)
	if err != nil {
		return fmt.Errorf("%s", message)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s", message)
	}
	var output []byte
	for len(raw) >= 8 && (raw[0] == 1 || raw[0] == 2) && raw[1] == 0 && raw[2] == 0 && raw[3] == 0 {
		size := int(binary.BigEndian.Uint32(raw[4:8]))
		if size > len(raw)-8 {
			break
		}
		output = append(output, raw[8:8+size]...)
		raw = raw[8+size:]
	}
	if len(output) == 0 {
		output = raw
	}
	if len(output) > 8192 {
		output = output[len(output)-8192:]
	}
	detail := strings.TrimSpace(consoleColor.ReplaceAllString(string(output), ""))
	if detail == "" {
		return fmt.Errorf("%s", message)
	}
	return fmt.Errorf("%s. Últimos registros:\n%s", message, detail)
}
