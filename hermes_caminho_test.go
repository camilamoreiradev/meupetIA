package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// criaArquivo — arquivo vazio (com as pastas) para simular uma instalação do Hermes.
func criaArquivo(t *testing.T, partes ...string) string {
	t.Helper()
	p := filepath.Join(partes...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAchaHermesLayoutNovo — o Hermes 0.21+ no Windows não tem mais
// hermes-agent\venv: o comando é hermes\bin\hermes.exe e o venv (com websockets, que a
// ponte usa) mora em hermes\installs\<id>\environments\<hash>\venv. Sem achar os dois,
// o chat do boneco dizia "Não encontrei o Hermes" com o Hermes instalado.
func TestAchaHermesLayoutNovo(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("PATH", "") // nada de achar o python/hermes do sistema por acaso
	exe := criaArquivo(t, base, "hermes", "bin", "hermes.exe")
	py := criaArquivo(t, base, "hermes", "installs", "bb5e", "environments", "04b2", "venv", "Scripts", "python.exe")

	if got := caminhoHermes(); got != exe {
		t.Errorf("caminhoHermes() = %q, queria %q", got, exe)
	}
	if got := caminhoPythonHermes(); got != py {
		t.Errorf("caminhoPythonHermes() = %q, queria %q", got, py)
	}
}

// TestAchaPythonComWebsockets — vários venvs em installs\…\environments, todos com o
// python.exe na mesma data (o uv grava assim). Vence o que tem websockets e, entre esses, a
// pasta mais nova — não o primeiro em ordem alfabética (30/10/2026: era um venv vazio).
func TestAchaPythonComWebsockets(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("PATH", "")
	envs := filepath.Join(base, "hermes", "installs", "bb5e", "environments")
	mesmaData := time.Date(2023, 12, 31, 21, 0, 0, 0, time.UTC)
	venv := func(nome string, pasta time.Time, ws bool) string {
		py := criaArquivo(t, envs, nome, "venv", "Scripts", "python.exe")
		_ = os.Chtimes(py, mesmaData, mesmaData)
		if ws {
			criaArquivo(t, envs, nome, "venv", "Lib", "site-packages", "websockets", "__init__.py")
		}
		_ = os.Chtimes(filepath.Join(envs, nome, "venv"), pasta, pasta)
		return py
	}
	dia := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	venv("04b2", dia(30), false) // o mais novo, mas vazio: não serve para a ponte
	venv("32c0", dia(22), true)
	quer := venv("b2a0", dia(29), true)
	venv("5883", dia(23), true)

	if got := caminhoPythonHermes(); got != quer {
		t.Errorf("caminhoPythonHermes() = %q, queria %q (o venv mais novo COM websockets)", got, quer)
	}
}

// TestAchaHermesLayoutAntigo — quem ainda tem o hermes-agent\venv continua igual.
func TestAchaHermesLayoutAntigo(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	t.Setenv("PATH", "")
	exe := criaArquivo(t, base, "hermes", "hermes-agent", "venv", "Scripts", "hermes.exe")
	py := criaArquivo(t, base, "hermes", "hermes-agent", "venv", "Scripts", "python.exe")
	criaArquivo(t, base, "hermes", "bin", "hermes.exe") // os dois layouts: o antigo manda

	if got := caminhoHermes(); got != exe {
		t.Errorf("caminhoHermes() = %q, queria %q", got, exe)
	}
	if got := caminhoPythonHermes(); got != py {
		t.Errorf("caminhoPythonHermes() = %q, queria %q", got, py)
	}
}
