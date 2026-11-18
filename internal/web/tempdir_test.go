package web

import (
	"os"
	"path/filepath"
	"testing"
)

// shortTempDir cria um diretório temporário de nome curto para abrigar o socket
// Unix do agente.
//
// Por que não t.TempDir(): sockaddr_un limita o caminho do socket a 108 bytes,
// e t.TempDir() embute o nome do teste no caminho. Em ambientes cujo TMPDIR já
// é longo — o sandbox de build do Nix usa /nix/var/nix/builds/nix-<pid>-<id> —
// o limite estoura e o bind falha com "invalid argument", um erro que não
// aparenta ter relação alguma com o teste.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// Falha explícita em vez de silenciosa: um pulo esconderia a perda de
	// cobertura, e a saída diz como contornar.
	if path := filepath.Join(dir, "updater.sock"); len(path) > 100 {
		t.Fatalf("caminho de socket longo demais para sockaddr_un (%d bytes): %s\nexporte um TMPDIR mais curto", len(path), path)
	}
	return dir
}
