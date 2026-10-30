package main

// Ponte QUENTE com o Hermes (o chat do boneco em ~3-8 s, não em ~30 s).
//
// Descoberta de 02/10/2026 (medida):
//   * `hermes -z` sobe um agente novo por mensagem  -> ~27-86 s por resposta;
//   * `hermes acp` mantém UM agente vivo e fala JSON-RPC por stdio -> ~3,8 s.
//
// Então o app sobe UMA vez a ponte Python (chat_bridge.py), que segura o agente
// quente, e conversa com ela por stdin/stdout (1 JSON por linha):
//
//	app -> {"prompt": "texto"}
//	app <- {"ok": true, "resposta": "...", "segundos": 3.8}
//
// Se a ponte não estiver de pé, cai na reserva lenta (`hermes -z`) - nunca fica mudo.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"sync"
	"syscall"
	"time"
)

var (
	ponteIn     io.WriteCloser
	ponteLinhas chan string // stdout da ponte, linha a linha — de UM leitor só (ligaPonte)
	ponteMu     sync.Mutex  // uma pergunta por vez
	ponteSeq    int         // id da pergunta em andamento (só muda com ponteMu)
	// a ESCRITA no stdin da ponte (e a troca de ponteIn): a resposta de uma aprovação é escrita
	// enquanto a pergunta segura ponteMu — as duas linhas não podem se misturar no cano
	ponteEscritaMu sync.Mutex

	// o processo da ponte: guardado para poder REINICIAR (painel Inteligência trocou a IA).
	// Antes era um sync.Once e o processo nem era guardado — subia uma vez e pronto.
	ponteVidaMu sync.Mutex
	ponteCmd    *exec.Cmd

	ponteModeloMu sync.Mutex
	ponteModelo   string // o modelo que a ponte diz estar usando (cabeçalho do chat)

	// escritos pelo leitor da ponte e lidos a cada pergunta: sempre com a trava
	ponteEstadoMu sync.Mutex
	ponteErro     string
	pontePronta   bool

	// prazoPonte — quanto o app espera cada linha da ponte (variável só para o teste
	// encurtar; o porquê dos 330 s está em perguntaPonte).
	prazoPonte = 330 * time.Second
)

// guardaModelo — a ponte manda o modelo no handshake e em cada resposta (vazio = não sabe,
// e não apaga o que já se sabia).
func guardaModelo(m string) {
	if m = strings.TrimSpace(m); m == "" {
		return
	}
	ponteModeloMu.Lock()
	ponteModelo = m
	ponteModeloMu.Unlock()
}

func modeloPonte() string {
	ponteModeloMu.Lock()
	defer ponteModeloMu.Unlock()
	return ponteModelo
}

// caminhoPonte — o script da ponte, ao lado do executável do app.
func caminhoPonte() string {
	cands := []string{}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "chat_bridge.py"))
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		cands = append(cands, filepath.Join(base, "petagent", "chat_bridge.py"))
	}
	for _, p := range cands {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// caminhoPythonHermes — o python do venv do Hermes (tem tudo que a ponte usa).
func caminhoPythonHermes() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		p := filepath.Join(base, "hermes", "hermes-agent", "venv", "Scripts", "python.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		// Hermes 0.21+ (Windows): o venv mora em installs\<id>\environments\<hash>\venv
		// (nome sorteado), e há vários. Vale o que TEM websockets (a ponte importa logo na
		// primeira linha) e, entre esses, a pasta mais nova.
		//
		// Medido em 30/10/2026: antes valia o python.exe mais novo, mas o uv grava TODOS com a
		// mesma data (31/12/2023) — empatava e vencia o primeiro em ordem alfabética, um venv
		// sem pacote nenhum. A ponte só subia porque o app herdava um PYTHONPATH do Hermes;
		// aberto de outro jeito, morria com "websockets não está instalado".
		achados, _ := filepath.Glob(filepath.Join(base, "hermes", "installs", "*",
			"environments", "*", "venv", "Scripts", "python.exe"))
		var maisNovo time.Time
		comWS := false
		p = ""
		for _, c := range achados {
			venv := filepath.Dir(filepath.Dir(c))
			st, err := os.Stat(venv)
			if err != nil {
				continue
			}
			_, errWS := os.Stat(filepath.Join(venv, "Lib", "site-packages", "websockets"))
			temWS := errWS == nil
			if p == "" || (temWS && !comWS) || (temWS == comWS && st.ModTime().After(maisNovo)) {
				maisNovo, comWS, p = st.ModTime(), temWS, c
			}
		}
		if p != "" {
			return p
		}
	}
	if p, err := exec.LookPath("python"); err == nil {
		return p
	}
	return ""
}

// sobePonte — liga a ponte (uma vez só). O agente leva ~10 s para ficar pronto;
// isso acontece no boot do app, fora do caminho da conversa.
func (a *App) sobePonte() {
	ponteVidaMu.Lock()
	defer ponteVidaMu.Unlock()
	if ponteCmd != nil {
		return
	}
	func() {
		// provedor que exige complemento do Hermes (inteligencia.go, garanteComplemento): quem
		// trocou para a Anthropic direto no Hermes, sem o painel, também fica coberto. ~1 s
		// quando já está instalado; falhando, a ponte sobe igual e o chat mostra o erro.
		if p, ok := provedorPorID(iaAgora().Provedor); ok {
			_, _ = garanteComplemento(p)
		}
		script, py := caminhoPonte(), caminhoPythonHermes()
		if script == "" || py == "" {
			marcaPonte(false, "Agente indisponível (script da ponte ou Python não encontrado)")
			return
		}
		cmd := exec.Command(py, script)
		// IA só do boneco (inteligencia.go): modelo e chave vão para a ponte pelo AMBIENTE,
		// nunca pela linha de comando; ela monta o espelho com eles
		cmd.Env = append(os.Environ(), envPonte()...)
		// a última conversa aberta: a ponte retoma ela em vez de começar do zero (conversas.go)
		if c := conversaAtual(); c != "" {
			cmd.Env = append(cmd.Env, "PETAGENT_CONVERSA="+c)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		}
		in, err := cmd.StdinPipe()
		if err != nil {
			marcaPonte(false, "Falha ao iniciar a ponte (stdin): "+err.Error())
			return
		}
		var out io.ReadCloser
		if out, err = cmd.StdoutPipe(); err != nil {
			marcaPonte(false, "Falha ao iniciar a ponte (stdout): "+err.Error())
			return
		}
		// o stderr da ponte vai para arquivo: quando ela morria calada (02/10/2026)
		// eu não tinha como saber por quê. ⚠️ Tem de ser ANTES do Start: o exec só
		// olha cmd.Stderr ao iniciar (antes era depois, e o arquivo nunca recebia nada).
		arq := filepath.Join(filepath.Dir(script), "ponte_erro.log")
		if f, errArq := os.OpenFile(arq, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); errArq == nil {
			cmd.Stderr = f
		}
		if err = cmd.Start(); err != nil {
			marcaPonte(false, "Falha ao iniciar a ponte: "+err.Error())
			return
		}
		logMsg("ponte: chat quente ligado (hermes acp via chat_bridge.py)")
		ponteCmd = cmd
		ligaPonte(in, out)
	}()
}

// reiniciaPonte — derruba e sobe de novo (a IA mudou). Fechar o stdin faz a ponte
// derrubar o backend dela (a árvore inteira) e sair; se não sair em 15 s, morre na marra.
// Espera a pergunta em andamento terminar (ponteMu).
func (a *App) reiniciaPonte() {
	ponteMu.Lock()
	ponteVidaMu.Lock()
	marcaPonte(false, "Reiniciando o agente")
	ponteModeloMu.Lock()
	ponteModelo = "" // o handshake novo traz o modelo novo
	ponteModeloMu.Unlock()
	ponteEscritaMu.Lock()
	if ponteIn != nil {
		_ = ponteIn.Close()
	}
	ponteEscritaMu.Unlock()
	if ponteCmd != nil {
		fim := make(chan struct{})
		go func(c *exec.Cmd) { _ = c.Wait(); close(fim) }(ponteCmd)
		select {
		case <-fim:
		case <-time.After(15 * time.Second):
			_ = ponteCmd.Process.Kill()
			logMsg("ponte: não saiu sozinha em 15 s — encerrada à força")
		}
		ponteCmd = nil
	}
	ponteEscritaMu.Lock()
	ponteIn, ponteLinhas = nil, nil
	ponteEscritaMu.Unlock()
	ponteVidaMu.Unlock()
	ponteMu.Unlock()
	logMsg("ponte: reiniciando (a IA mudou)")
	a.sobePonte()
}

func estadoPonte() (pronta bool, erro string) {
	ponteEstadoMu.Lock()
	defer ponteEstadoMu.Unlock()
	return pontePronta, ponteErro
}

func marcaPonte(pronta bool, erro string) {
	ponteEstadoMu.Lock()
	pontePronta, ponteErro = pronta, erro
	ponteEstadoMu.Unlock()
	if erro != "" {
		logMsg("ponte: " + erro)
	}
}

// ligaPonte — passa a conversar com a ponte por estes canos.
//
// UM LEITOR SÓ para o stdout da ponte. Antes, cada pergunta criava a própria goroutine
// de leitura e, quando o prazo estourava, ela ficava ÓRFÃ lendo o mesmo bufio.Reader
// que a da pergunta seguinte. A órfã comia uma linha (um progresso, por exemplo) e a
// resposta ATRASADA da pergunta anterior era entregue como resposta da nova — o dono
// perguntava B e lia a resposta de A (ver ponte_test.go). Agora só esta goroutine lê;
// cada pergunta leva um id e perguntaPonte descarta o que for de outra.
func ligaPonte(in io.WriteCloser, out io.Reader) {
	linhas := make(chan string, 256)
	ponteMu.Lock()
	ponteEscritaMu.Lock()
	ponteIn, ponteLinhas = in, linhas
	ponteEscritaMu.Unlock()
	ponteMu.Unlock()
	go func() {
		defer close(linhas)
		r := bufio.NewReaderSize(out, 1<<20)
		// espera o handshake: a ponte só vale quando diz "pronto: true"
		linha, err := r.ReadString('\n')
		if err != nil {
			marcaPonte(false, "A ponte parou antes de confirmar a conexão: "+err.Error())
			return
		}
		var d struct {
			Pronto   bool   `json:"pronto"`
			Erro     string `json:"erro"`
			Modelo   string `json:"modelo"`
			Conversa string `json:"conversa"`
		}
		if json.Unmarshal([]byte(linha), &d) != nil {
			marcaPonte(false, "Resposta inicial da ponte ilegível: "+strings.TrimSpace(linha))
			return
		}
		if !d.Pronto {
			marcaPonte(false, "A ponte não conseguiu iniciar o agente: "+d.Erro)
			return
		}
		guardaModelo(d.Modelo)
		guardaConversa(d.Conversa)
		marcaPonte(true, "")
		logMsg("ponte: agente quente confirmado (handshake ok)")
		for {
			linha, err := r.ReadString('\n')
			if strings.TrimSpace(linha) != "" {
				linhas <- linha
			}
			if err != nil {
				marcaPonte(false, "A ponte foi encerrada: "+err.Error())
				return
			}
		}
	}()
}

// respostaPonte — a linha FINAL de um pedido à ponte (pergunta ou operação de conversa).
type respostaPonte struct {
	ID        int             `json:"id"` // eco do id do pedido (ponte antiga não manda: 0)
	OK        bool            `json:"ok"`
	Pronto    bool            `json:"pronto"`
	Resposta  string          `json:"resposta"`
	Progresso string          `json:"progresso"` // "texto", "passo", "aprovacao"… — NÃO é a resposta
	Aprovacao json.RawMessage `json:"aprovacao"` // o pedido de aprovação (progresso "aprovacao")
	Texto     string          `json:"texto"`
	Erro      string          `json:"erro"`
	Segundos  float64         `json:"segundos"`
	Modelo    string          `json:"modelo"`
	Conversa  string          `json:"conversa"` // id guardado da conversa em que a ponte respondeu
	Dados     json.RawMessage `json:"dados"`    // resultado de uma operação ({"op": ...})
}

// perguntaPonte — manda uma pergunta (na conversa atual da ponte) e espera a resposta.
func (a *App) perguntaPonte(texto string) (string, bool) {
	r, ok := a.pedidoPonte(map[string]any{"prompt": texto}, true)
	return r.Resposta, ok
}

// pedidoPonte — manda um pedido e espera a linha final dele.
//
// Pergunta: ok=true quer dizer que a ponte cuidou dela — a resposta é o texto OU um aviso
// para a tela. ok=false: a ponte está fora do ar e vale a reserva lenta. Se a ponte está de
// pé e só demorou, NÃO é caso de reserva: ela segue trabalhando na pergunta e a reserva
// repetiria a mesma pergunta, gastando créditos em dobro (13/10/2026).
//
// Operação ({"op": ...}, as conversas): ok=false só com a ponte fora do ar ou OCUPADA —
// esperar=false não fica na fila atrás de uma pergunta que pode levar minutos (a lista de
// conversas não pode congelar enquanto o pet pensa).
func (a *App) pedidoPonte(pedido map[string]any, esperar bool) (respostaPonte, bool) {
	_, operacao := pedido["op"]
	if pronta, erro := estadoPonte(); !pronta {
		if operacao {
			return respostaPonte{Erro: "O agente ainda não está pronto."}, false
		}
		if erro != "" {
			logMsg("ponte: indisponível (" + erro + ") — usando a reserva lenta")
		} else {
			logMsg("ponte: ainda não confirmada — usando a reserva lenta")
		}
		return respostaPonte{}, false
	}
	if esperar {
		ponteMu.Lock()
	} else if !ponteMu.TryLock() {
		return respostaPonte{Erro: "ocupado"}, false
	}
	defer ponteMu.Unlock()

	ponteSeq++
	id := ponteSeq
	pedido["id"] = id
	bruto, _ := json.Marshal(pedido)
	if err := escrevePonte(bruto); err != nil {
		logMsg("ponte: falha ao escrever (" + err.Error() + ") — reserva lenta")
		return respostaPonte{Erro: err.Error()}, false
	}
	t0 := time.Now()
	// lê até achar uma RESPOSTA de verdade: a ponte manda um frame de handshake
	// antes, e um frame extra não pode ser confundido com resposta (02/10/2026)
	for tentativa := 0; tentativa < 4; tentativa++ {
		select {
		case linha, aberta := <-ponteLinhas:
			if !aberta {
				logMsg("ponte: encerrou no meio da pergunta — reserva lenta")
				return respostaPonte{Erro: "A conexão com o agente foi encerrada."}, false
			}
			var d respostaPonte
			if err := json.Unmarshal([]byte(linha), &d); err != nil {
				txt := strings.TrimSpace(linha)
				if txt == "" {
					continue
				}
				logMsg("ponte: linha fora do formato — usando como texto")
				return respostaPonte{OK: true, Resposta: txt}, true
			}
			// linha de OUTRA pergunta: a que estourou o prazo e que a ponte só terminou
			// agora. Não é desta e não gasta tentativa.
			if d.ID != 0 && d.ID != id {
				if d.Progresso == "" {
					logMsg(fmt.Sprintf("ponte: resposta atrasada da pergunta %d descartada", d.ID))
					guardaConversa(d.Conversa) // ainda assim diz em que conversa a ponte está
				}
				tentativa--
				continue
			}
			// LINHA DE PROGRESSO (10/10/2026) — o dono: "eu não fico nunca no vácuo aqui".
			//
			// Medido: no Telegram cada passo do agente vira uma mensagem (11 ferramentas = 11
			// mensagens) e ele nunca fica esperando no escuro; aqui eram 9 ferramentas virando
			// UMA mensagem no fim — 224 s olhando um balão parado. A ponte agora emite o que
			// está acontecendo ENQUANTO acontece; isto repassa para a tela e NÃO consome
			// tentativa (a linha não é resposta).
			if d.Progresso != "" {
				if a != nil && a.ctx != nil {
					switch d.Progresso {
					case "aprovacao": // o cartão do chat (RespondeAprovacao devolve a escolha)
						runtime.EventsEmit(a.ctx, "chat-aprovacao", string(d.Aprovacao))
					case "aprovacao-fim": // o Hermes retirou a pergunta: o cartão sai
						runtime.EventsEmit(a.ctx, "chat-aprovacao-fim", d.Texto)
					default:
						runtime.EventsEmit(a.ctx, "chat-progresso", map[string]string{
							"tipo": d.Progresso, "texto": d.Texto,
						})
					}
				}
				tentativa-- // progresso não gasta tentativa: só as 4 leituras "de verdade" contam
				continue
			}
			if d.Pronto && !d.OK { // handshake antigo/atrasado: não é resposta
				continue
			}
			if d.Pronto && d.Resposta == "" {
				continue
			}
			if operacao {
				return d, true // erro de operação vai para a tela (d.Erro), sem reserva
			}
			if !d.OK {
				if d.Erro != "" {
					logMsg("ponte: erro do agente (" + d.Erro + ") — reserva lenta")
				}
				return d, false
			}
			guardaModelo(d.Modelo)
			guardaConversa(d.Conversa)
			logMsg(fmt.Sprintf("ponte: resposta quente em %.1fs", time.Since(t0).Seconds()))
			return d, true
		case <-time.After(prazoPonte):
			// O APP TEM DE ESPERAR MAIS QUE A PONTE.
			//
			// Defeito medido em 10/10/2026 (relato do dono: "fiz uma pergunta simples e ele levou
			// 205 segundos"): aqui o prazo era 150 s, mas `chat_bridge.py::perguntar` espera 300 s
			// e, se o canal quente cair, RECONECTA uma vez e repete. Com 150 s o app desistia
			// ANTES da ponte terminar, caía na reserva lenta (`hermes -z`) e a reconexão nunca
			// chegava a rodar. Somando: 150 s de espera + ~56 s da reserva = os 205 s medidos no
			// log real (10:33:12 "tempo esgotado (150s)" → 10:34:08 "reserva respondeu").
			//
			// Com 330 s a ponte tem a janela dela inteira: ou responde quente, ou reconecta
			// sozinha — e a reserva lenta volta a ser ÚLTIMO recurso em vez de caminho comum.
			//
			// SEM RESERVA AQUI (13/10/2026, para não queimar créditos): estourar o prazo não
			// para a ponte — o agente segue trabalhando na pergunta (até o teto de iterações).
			// Cair na reserva rodava a MESMA pergunta num segundo agente, as duas gastando ao
			// mesmo tempo. Agora o boneco avisa e a pessoa decide se pergunta de novo.
			logMsg("ponte: tempo esgotado (330s) — sem reserva (a ponte segue na pergunta)")
			return respostaPonte{OK: true, Resposta: msgPonteDemorou, Erro: msgPonteDemorou}, true
		}
	}
	// ponte de pé, só confusa: mesma regra do tempo esgotado — nada de repetir a pergunta
	logMsg("ponte: frames demais sem resposta — sem reserva")
	return respostaPonte{OK: true, Resposta: msgPonteDemorou, Erro: msgPonteDemorou}, true
}

// escrevePonte — uma linha no stdin da ponte (sob ponteEscritaMu: ver a declaração).
func escrevePonte(bruto []byte) error {
	ponteEscritaMu.Lock()
	defer ponteEscritaMu.Unlock()
	if ponteIn == nil {
		return fmt.Errorf("a ponte não está ligada")
	}
	_, err := ponteIn.Write(append(bruto, '\n'))
	return err
}

// escolhasAprovacao — as que o Hermes aceita (o mesmo ESCOLHAS_APROVACAO do chat_bridge.py)
var escolhasAprovacao = map[string]bool{"once": true, "session": true, "always": true, "deny": true}

// RespondeAprovacao — a escolha da dona no cartão de aprovação do chat (30/10/2026).
//
// O Hermes só roda comando perigoso com o "sim" de alguém e pergunta à ponte (ver o topo do
// chat_bridge.py). A pergunta está EM ANDAMENTO — segura ponteMu até o agente terminar, e ele
// só termina depois desta resposta. Por isso aqui NÃO se pega ponteMu: a linha vai direto
// para a ponte, que a encaminha ao Hermes sem passar pela fila de perguntas.
func (a *App) RespondeAprovacao(pedido, escolha string) string {
	pedido = strings.TrimSpace(pedido)
	if pedido == "" || !escolhasAprovacao[escolha] {
		return "Resposta de aprovação inválida."
	}
	bruto, _ := json.Marshal(map[string]string{"aprovacao": pedido, "escolha": escolha})
	if err := escrevePonte(bruto); err != nil {
		logMsg("ponte: não consegui entregar a aprovação (" + err.Error() + ")")
		return "Não consegui entregar a resposta ao agente: " + err.Error()
	}
	logMsg("ponte: aprovação " + pedido + " → " + escolha)
	return ""
}

const msgPonteDemorou = "A resposta está demorando demais. Parei de esperar para não gastar " +
	"créditos em dobro (o Hermes ainda pode estar terminando). Tente novamente daqui a pouco."
