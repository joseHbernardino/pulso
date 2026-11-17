package containerupdate

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func dataDirectory(c *container) string {
	dir := "/var/lib/pulso"
	for _, field := range []string{"Entrypoint", "Cmd"} {
		if args, ok := c.Config[field].([]any); ok {
			for i := 0; i < len(args); i++ {
				arg := fmt.Sprint(args[i])
				if arg == "--data-dir" && i+1 < len(args) {
					dir = fmt.Sprint(args[i+1])
					i++
				} else if strings.HasPrefix(arg, "--data-dir=") {
					dir = strings.TrimPrefix(arg, "--data-dir=")
				}
			}
		}
	}
	if !path.IsAbs(dir) {
		cwd, _ := c.Config["WorkingDir"].(string)
		dir = path.Join(cwd, dir)
	}
	return path.Clean(dir)
}

// Only Pulso's recovery table is backed up here. Application data/SQLite migrations are not rewound.
func (d *Engine) recoverySnapshot(ctx context.Context, c *container) ([]byte, error) {
	resp, err := d.call(ctx, "GET", "/containers/"+c.ID+"/archive?path="+url.QueryEscape(path.Join(dataDirectory(c), "dump.json")), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	archive, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(archive) > 4<<20 {
		return nil, fmt.Errorf("o estado de recuperação excede o limite de 4 MiB para atualização pelo painel")
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	header, err := reader.Next()
	if err != nil {
		return nil, err
	}
	if header.Name != "dump.json" || header.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("arquivo de recuperação inválido")
	}
	contents, err := io.ReadAll(reader)
	if err != nil || !json.Valid(contents) {
		return nil, fmt.Errorf("estado de recuperação inválido")
	}
	if _, err = reader.Next(); err != io.EOF {
		return nil, fmt.Errorf("snapshot de recuperação contém arquivos inesperados")
	}
	return archive, nil
}
func (d *Engine) restoreSnapshot(ctx context.Context, c *container, archive []byte) error {
	if len(archive) == 0 {
		return fmt.Errorf("snapshot de recuperação ausente")
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", "http://docker"+d.prefix+"/containers/"+c.ID+"/archive?copyUIDGID=true&path="+url.QueryEscape(dataDirectory(c)), bytes.NewReader(archive))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("restaurar estado das aplicações: docker HTTP %d", resp.StatusCode)
	}
	return nil
}
