package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ── Win32 ─────────────────────────────────────────────────────────────
var (
	user32 = syscall.NewLazyDLL("user32.dll")

	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	procGetWindowLongPtr   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr   = user32.NewProc("SetWindowLongPtrW")
	procEnumWindows        = user32.NewProc("EnumWindows")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procClientToScreen     = user32.NewProc("ClientToScreen")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procGetClientRect      = user32.NewProc("GetClientRect")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
)

const (
	gwlExStyle      = -20
	wsExTransparent = 0x00000020 // click-through
	wsExLayered     = 0x00080000 // obrigatório p/ janela translúcida

	// Tamanho da janela do boneco (em DIP). NUNCA fullscreen: se o click-through
	// falhar, o estrago tem que caber num canto da tela — não na tela toda.
	janelaW = 560
	janelaH = 720
)

type point struct{ X, Y int32 }
type rect struct{ L, T, R, B int32 }

// gwlpIndex — -20 (GWL_EXSTYLE) como uintptr. Feito por variável de propósito:
// constante negativa convertida direto para uintptr não compila ("overflows uintptr").
func gwlpIndex() uintptr {
	i := int32(gwlExStyle)
	return uintptr(uint32(i))
}

// gwlpIndexStyle — -16 (GWL_STYLE), mesma razão.
func gwlpIndexStyle() uintptr {
	i := int32(-16)
	return uintptr(uint32(i))
}

// tiraBarraTitulo — remove a barra de título/bordas via Win32 DEPOIS que a janela
// já nasceu transparente.
//
// ⚠️ ACHADO (medido 01/10/2026): a opção Frameless do Wails deixa a janela OPACA
// (fundo branco). Criando a janela normal (transparente ✓) e tirando a barra por
// SetWindowLongPtr, a transparência sobrevive.
func tiraBarraTitulo(hwnd syscall.Handle) {
	const (
		wsCaption       = 0x00C00000
		wsThickFrame    = 0x00040000
		wsMinimizeBox   = 0x00020000
		wsMaximizeBox   = 0x00010000
		wsSysMenu       = 0x00080000
		swpFrameChanged = 0x0020
		swpNoMove       = 0x0002
		swpNoSize       = 0x0001
		swpNoZOrder     = 0x0004
		swpNoActivate   = 0x0010
	)
	st, _, _ := procGetWindowLongPtr.Call(uintptr(hwnd), gwlpIndexStyle())
	novo := st &^ uintptr(wsCaption|wsThickFrame|wsMinimizeBox|wsMaximizeBox|wsSysMenu)
	procSetWindowLongPtr.Call(uintptr(hwnd), gwlpIndexStyle(), novo)
	procSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0,
		uintptr(swpFrameChanged|swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate))
	logMsg("barra de titulo removida via Win32 (transparencia preservada)")
}

// tirarBarra — remove a barra de título por Win32 DEPOIS que a janela nasceu.
// É ESTE o caminho correto (validado pelo dono): janela criada com barra
// (nasce transparente) e a barra removida em seguida → sem barra, sem moldura,
// transparente. Deixar a barra visível é feio; usar Frameless do Wails é branco.
var tirarBarra = true

// Log — o frontend manda recado para o log do app (depuração do boneco/UI).
func (a *App) Log(texto string) string {
	logMsg("js: " + texto)
	return "ok"
}

// offCliente — [0]=borda esquerda, [1]=topo não-cliente, [2]=altura do cliente (físico).
var offCliente [3]int

// alinhaCliente — truque de overlay: a barra de título é o preço da transparência
// neste Wails (Frameless ou remover a barra = janela branca). Então mantemos a
// barra e a jogamos PARA FORA da tela, deixando só a área do cliente visível.
func (a *App) alinhaCliente() {
	a.mu.Lock()
	hwnd := a.hwnd
	a.mu.Unlock()
	if hwnd == 0 {
		return
	}
	var wr rect
	procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr)))
	cli := point{0, 0}
	procClientToScreen.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cli)))
	offCliente[0] = int(cli.X - wr.L) // borda esquerda
	offCliente[1] = int(cli.Y - wr.T) // barra de título + borda
	// Depois de tirar a barra, o cliente ainda não alcança o rodapé da janela
	// (sobra uma área não-cliente invisível). Medimos para compensar embaixo.
	var cr rect
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	offCliente[2] = int(cr.B - cr.T) // altura do cliente
	logMsg(fmt.Sprintf("offsets do cliente medidos: dx=%d dy=%d altura_cliente=%d",
		offCliente[0], offCliente[1], offCliente[2]))
}

// posicionaCliente — põe o CLIENTE em (x,y) físicos da tela.
func (a *App) posicionaCliente(x, y int) {
	if a.ctx == nil {
		return
	}
	runtime.WindowSetPosition(a.ctx, x-offCliente[0], y-offCliente[1])
}

// App — estado do aplicativo.
type App struct {
	ctx  context.Context
	hwnd syscall.Handle

	mu           sync.Mutex
	hitRects     []rect // retângulos (físicos) onde o clique NÃO deve atravessar
	temRects     bool   // já recebemos retângulos? (sem isso, NUNCA forçar click-through)
	clickThrough bool
	balance      string
	balanceAt    time.Time
	hidden       bool
	ultimoEstado string // p/ detectar transições (gateway mudo → voltou etc.)
	hideSeq      int    // geração do "esconder N min" (só o mais novo manda)
}

// aviso — manda um balãozinho para a tela (toast) e registra no log.
func (a *App) aviso(ico, titulo, texto string) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "aviso", map[string]string{
		"icone": ico, "titulo": titulo, "texto": texto,
	})
	logMsg("aviso: " + titulo + " | " + texto)
}

func NewApp() *App { return &App{} }

// ── ciclo de vida ─────────────────────────────────────────────────────
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.posicionar()
	go a.acomodarJanela() // acha o hwnd + liga o click-through (com rede de segurança)
	go a.atalhoGlobal()   // Ctrl+Alt+Q desliga de qualquer lugar
	go a.hitTestLoop()
	go a.stateLoop()
	go a.escondeDuranteCaptura() // some enquanto a Ferramenta de Captura está aberta
	go a.emailLoop()             // triagem de e-mail (lê as contas por IMAP, só leitura)
	go a.tarefasLoop()           // lembretes das tarefas (balão + notificação do Windows)
	go a.atalhoTarefas()         // Ctrl+Alt+T: anotar uma tarefa de qualquer lugar
	go a.warmBalance()
}

// atalhoGlobal — Ctrl+Alt+Q encerra o PetAgent, funcione o resto como funcionar.
// Rede de segurança nível 1: o usuário NUNCA pode ficar sem como desligar.
func (a *App) atalhoGlobal() {
	for {
		time.Sleep(120 * time.Millisecond)
		ctrl, _, _ := procGetAsyncKeyState.Call(0x11) // VK_CONTROL
		alt, _, _ := procGetAsyncKeyState.Call(0x12)  // VK_MENU
		q, _, _ := procGetAsyncKeyState.Call(0x51)    // tecla Q
		if ctrl&0x8000 != 0 && alt&0x8000 != 0 && q&0x8000 != 0 {
			logMsg("atalho Ctrl+Alt+Q: desligando por seguranca")
			a.QuitApp()
			return
		}
	}
}

// systemDPI — 96 = 100%. WindowSetPosition espera DIP; GetCursorPos devolve físico.
func systemDPI() int {
	u := syscall.NewLazyDLL("user32.dll")
	p := u.NewProc("GetDpiForSystem")
	if r, _, _ := p.Call(); r != 0 {
		return int(r)
	}
	return 96
}

// posicionar — ancora a janela no canto inferior esquerdo (em DIP).
func (a *App) posicionar() {
	_, sh := screenSize()
	dpi := systemDPI()
	escala := float64(dpi) / 96.0
	dipH := float64(sh) / escala
	y := int(dipH) - janelaH - 48
	if y < 0 {
		y = 0
	}
	runtime.WindowSetPosition(a.ctx, 16, y)
}

// acomodarJanela — no OnStartup a janela do Wails AINDA NÃO EXISTE, então findOwnHwnd
// devolve 0 e o click-through nunca é aplicado (lição aprendida do jeito ruim).
// Aqui: tenta por até 20 s. Se não achar, ESCONDE a janela. Overlay cobrindo a tela é proibido.
func (a *App) acomodarJanela() {
	for i := 0; i < 40; i++ {
		if h := findOwnHwnd(); h != 0 {
			a.mu.Lock()
			a.hwnd = h
			a.mu.Unlock()
			a.setClickThrough(false) // aplica o estilo...
			a.setClickThrough(true)  // ...e já entra em modo "deixa passar"
			// reafirma o fundo transparente (o WebView2 às vezes acorda opaco)
			runtime.WindowSetBackgroundColour(a.ctx, 0, 0, 0, 0)
			if tirarBarra {
				// tira a barra DEPOIS de a janela existir transparente (Frameless do
				// Wails pinta branco — ver comentário em tiraBarraTitulo)
				tiraBarraTitulo(h)
				runtime.WindowSetBackgroundColour(a.ctx, 0, 0, 0, 0)
				a.setClickThrough(false)
				a.setClickThrough(true)
			}
			// vive na BANDEJA: sem botão na barra de tarefas e fora do Alt+Tab
			foraDaBarraDeTarefas(h)
			// e fora de QUALQUER captura — sem isso a ferramenta do Windows não
			// conseguia mais recortar uma janela (a nossa cobria a tela toda)
			foraDoPrint(h)
			a.alinhaCliente()     // mede barra/borda p/ alinhar o cliente na área útil
			go sobeServidorPets() // miniaturas dos bonecos p/ o painel de escolha
			go a.sobePonte()      // chat QUENTE (hermes acp): resposta em segundos
			logMsg(fmt.Sprintf("janela acomodada: hwnd=%x (%d tentativas)", h, i+1))
			a.aplicarTelaSalva() // se o usuário já escolheu tela/canto, aplica agora
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	logMsg("FALHA: hwnd nao encontrado em 20s - escondendo por seguranca")
	a.HideAll()
}

// ── janela: descoberta + click-through ────────────────────────────────
func findOwnHwnd() syscall.Handle {
	pid := uint32(os.Getpid())
	var found syscall.Handle
	cb := syscall.NewCallback(func(hwnd syscall.Handle, lparam uintptr) uintptr {
		var wpid uint32
		procGetWindowThreadPID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wpid)))
		if wpid == pid {
			if v, _, _ := procIsWindowVisible.Call(uintptr(hwnd)); v != 0 {
				found = hwnd
				return 0 // para
			}
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return found
}

func (a *App) setClickThrough(on bool) {
	a.mu.Lock()
	ja := a.clickThrough
	a.clickThrough = on
	hwnd := a.hwnd
	a.mu.Unlock()
	if hwnd == 0 || ja == on {
		return
	}
	logMsg(fmt.Sprintf("click-through=%v", on))
	style, _, _ := procGetWindowLongPtr.Call(uintptr(hwnd), gwlpIndex())
	nv := style | wsExLayered
	if on {
		nv |= wsExTransparent
	} else {
		nv &^= wsExTransparent
	}
	procSetWindowLongPtr.Call(uintptr(hwnd), gwlpIndex(), nv)
}

// hitTestLoop — a cada 60 ms decide se o clique deve atravessar a janela.
//
// REGRA DE SEGURANÇA (invertida de propósito, v0.2): enquanto NÃO soubermos onde
// estão os bonecos, a janela fica INTERATIVA — assim o usuário sempre consegue
// fechar no X. A versão anterior fazia o contrário e travou o PC do dono.
func (a *App) hitTestLoop() {
	inicio := time.Now()
	ultimoAviso := time.Now()
	for {
		time.Sleep(60 * time.Millisecond)
		a.mu.Lock()
		rs := append([]rect(nil), a.hitRects...)
		tem := a.temRects
		hidden := a.hidden
		hwnd := a.hwnd
		a.mu.Unlock()

		if !tem {
			// Overlay de TELA INTEIRA: sem saber onde está o boneco, o clique ATRAVESSA
			// (o desktop fica sempre utilizável — regra de ouro depois do incidente).
			// Sair do app nunca dependeu de clicar na janela: Ctrl+Alt+Q, systray
			// e PARAR-EMERGENCIA.cmd continuam valendo.
			a.setClickThrough(true)
			if time.Since(inicio) > 12*time.Second {
				logMsg("sem hit rects em 12s - escondendo por seguranca")
				a.HideAll()
				return
			}
			continue
		}
		if hidden {
			a.setClickThrough(true)
			continue
		}
		if hwnd == 0 {
			continue
		}
		var p point
		if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
			continue
		}
		// ⚠️ O BUG DA v0.2: GetCursorPos devolve coordenada de TELA, e os retângulos
		// do frontend são relativos ao CLIENTE da janela. Comparar direto = nunca
		// acertar o boneco = click-through permanente. ClientToScreen resolve.
		cli := point{0, 0}
		procClientToScreen.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cli)))
		relX := p.X - cli.X
		relY := p.Y - cli.Y

		dentro := false
		for _, r := range rs {
			if relX >= r.L && relX <= r.R && relY >= r.T && relY <= r.B {
				dentro = true
				break
			}
		}
		// Área não-cliente (barra de título/bordas): SEMPRE clicável — é a saída
		// de emergência do usuário e não pode depender do click-through.
		if !dentro {
			var wr rect
			if ok, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr))); ok != 0 {
				if p.X >= wr.L && p.X <= wr.R && p.Y >= wr.T && p.Y < cli.Y {
					dentro = true
				}
			}
		}
		if dentro && time.Since(ultimoAviso) > 3*time.Second {
			ultimoAviso = time.Now()
			logMsg(fmt.Sprintf("cursor na area clicavel: rel=(%d,%d) rects=%d", relX, relY, len(rs)))
		}
		a.setClickThrough(!dentro)
	}
}

// ── telemetria (o que dá vida ao boneco) ──────────────────────────────
type PetState struct {
	Agent     string `json:"agent"`
	State     string `json:"state"` // idle | pensando | dormindo | erro | offline
	Detail    string `json:"detail"`
	Heartbeat int    `json:"heartbeat"` // s desde o último heartbeat do gateway Hermes
	Balance   string `json:"balance"`
	Hwnd      string `json:"hwnd"`
	Version   string `json:"version"`
}

func hermesDir() string {
	lad := os.Getenv("LOCALAPPDATA")
	return filepath.Join(lad, "hermes")
}

// heartbeatAge — segundos desde o último heartbeat do gateway Hermes.
func heartbeatAge() int {
	p := filepath.Join(hermesDir(), "state", "gateway.heartbeat")
	fi, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return int(time.Since(fi.ModTime()).Seconds())
}

func envKey(path, nome string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?m)^\s*` + nome + `\s*=\s*['"]?([^'"\s]+)`)
	m := re.FindStringSubmatch(string(b))
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// balanceText — saldo ao vivo da conta que o Hermes usa (compartilhada). Nunca imprime a
// chave. DeepSeek era a única conhecida; em 22/10/2026 a instalação da dona usava a
// OpenRouter (só OPENROUTER_API_KEY no .env) e o chat mostrava "chave não encontrada" para
// sempre. Agora tenta as duas, na ordem.
//
// 22/10/2026 (painel Inteligência): o saldo é o da IA que o boneco USA de fato — a do
// Hermes ou a só do boneco (iaAgora, inteligencia.go). Provedor sem rota de saldo não
// inventa número.
func balanceText(_ int) string {
	if e := iaAgora(); e.Chave != "" {
		if p, ok := provedorPorID(e.Provedor); ok {
			if !p.Saldo {
				return "Saldo não informado pelo " + p.Nome
			}
			return saldoDoProvedor(p.ID, e.Chave)
		}
	}
	// config do Hermes sem provedor conhecido (auto, custom…): tenta as chaves que existirem
	env := filepath.Join(hermesDir(), ".env")
	if key := envKey(env, "DEEPSEEK_API_KEY"); key != "" {
		body, err := pedeSaldo("https://api.deepseek.com/user/balance", key)
		if err != nil {
			return "Saldo indisponível (sem conexão)"
		}
		return lerSaldoDeepSeek(body)
	}
	if key := envKey(env, "OPENROUTER_API_KEY"); key != "" {
		body, err := pedeSaldo(urlCreditosOpenRouter, key)
		if err != nil {
			return "Saldo indisponível (sem conexão)"
		}
		return lerSaldoOpenRouter(body)
	}
	return "Saldo indisponível (chave não encontrada)"
}

// saldoDoProvedor — US$ do provedor (só os que informam: OpenRouter e DeepSeek).
func saldoDoProvedor(id, key string) string {
	url, ler := "https://api.deepseek.com/user/balance", lerSaldoDeepSeek
	if id == "openrouter" {
		url, ler = urlCreditosOpenRouter, lerSaldoOpenRouter
	}
	body, err := pedeSaldo(url, key)
	if err != nil {
		return "Saldo indisponível (sem conexão)"
	}
	return ler(body)
}

// urlCreditosOpenRouter — variável só para o teste apontar para um servidor local
var urlCreditosOpenRouter = "https://openrouter.ai/api/v1/credits"

func pedeSaldo(url, key string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	cli := &http.Client{Timeout: 25 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func lerSaldoDeepSeek(body []byte) string {
	var d struct {
		Available bool `json:"is_available"`
		Infos     []struct {
			Currency string `json:"currency"`
			Total    string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if json.Unmarshal(body, &d) != nil || len(d.Infos) == 0 {
		return "Saldo indisponível (resposta inesperada do provedor)"
	}
	for _, i := range d.Infos {
		if i.Currency == "USD" {
			return "US$ " + i.Total
		}
	}
	return d.Infos[0].Currency + " " + d.Infos[0].Total
}

// OpenRouter: /api/v1/credits devolve o comprado e o usado; o saldo é a diferença.
func lerSaldoOpenRouter(body []byte) string {
	var d struct {
		Data *struct {
			Total *float64 `json:"total_credits"`
			Usado *float64 `json:"total_usage"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &d) != nil || d.Data == nil || d.Data.Total == nil || d.Data.Usado == nil {
		return "Saldo indisponível (resposta inesperada do provedor)"
	}
	return fmt.Sprintf("US$ %.2f", *d.Data.Total-*d.Data.Usado)
}

// saldoSemRotulo — o saldo para vir depois de um rótulo "Saldo: ": "US$ 4.82" fica como
// está; "Saldo indisponível (...)" vira "indisponível (...)", sem repetir a palavra.
func saldoSemRotulo(b string) string {
	if r := strings.TrimPrefix(b, "Saldo "); r != b {
		return r
	}
	return b
}

func (a *App) warmBalance() {
	a.mu.Lock()
	a.balance = balanceText(0)
	a.balanceAt = time.Now()
	a.mu.Unlock()
}

func (a *App) stateLoop() {
	for {
		age := heartbeatAge()
		a.mu.Lock()
		bal := a.balance
		if time.Since(a.balanceAt) > 10*time.Minute {
			a.mu.Unlock()
			go a.warmBalance()
			a.mu.Lock()
		}
		hw := a.hwnd
		a.mu.Unlock()

		st := PetState{
			Agent: "hermes", State: "idle", Balance: bal,
			Heartbeat: age, Hwnd: fmt.Sprintf("%x", hw),
			Version: "0.1.0-mvp",
		}
		switch {
		case age < 0:
			st.State, st.Detail = "erro", "Arquivo de heartbeat do gateway não encontrado"
		case age < 120:
			st.State, st.Detail = "idle", fmt.Sprintf("Gateway ativo (último sinal há %d s)", age)
		case age < 600:
			st.State, st.Detail = "pensando", fmt.Sprintf("Gateway sem sinal há %d s", age)
		default:
			st.State, st.Detail = "dormindo", fmt.Sprintf("Gateway sem resposta há %d min", age/60)
		}
		if num := strings.TrimPrefix(bal, "US$ "); strings.HasPrefix(bal, "US$ ") {
			if f, err := parseFloat(num); err == nil && f < 3.0 {
				st.State, st.Detail = "erro", "Saldo abaixo de US$ 3"
			}
		}
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "pet-state", st)
		}

		// ── transições relevantes viram "balãozinho na tela" (aviso) ──
		a.mu.Lock()
		ant := a.ultimoEstado
		a.ultimoEstado = st.State
		a.mu.Unlock()
		if ant != "" && ant != st.State {
			switch {
			case ant == "dormindo" && st.State == "idle":
				a.aviso("✅", "Gateway restabelecido", "O gateway estava sem resposta e voltou a funcionar (último sinal há "+fmt.Sprint(age)+" s).")
			case st.State == "erro":
				a.aviso("⚠️", "Atenção", st.Detail+".")
			case st.State == "dormindo":
				a.aviso("🔌", "Gateway sem resposta", st.Detail+".")
			}
		}
		time.Sleep(15 * time.Second)
	}
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &f)
	return f, err
}

// ── bindings (chamados pelo frontend) ─────────────────────────────────
// SetHitRects — frontend informa onde estão bonecos/balões (pixels físicos).
func (a *App) SetHitRects(js string) {
	var rs []rect
	if json.Unmarshal([]byte(js), &rs) != nil {
		logMsg("SetHitRects: JSON invalido")
		return
	}
	a.mu.Lock()
	mudou := len(rs) != len(a.hitRects)
	primeiro := !a.temRects
	a.hitRects = rs
	a.temRects = true
	a.mu.Unlock()
	if mudou || primeiro {
		a.mu.Lock()
		hw := a.hwnd
		a.mu.Unlock()
		cli := point{0, 0}
		if hw != 0 {
			procClientToScreen.Call(uintptr(hw), uintptr(unsafe.Pointer(&cli)))
		}
		alvo := ""
		if len(rs) > 0 {
			r := rs[0]
			alvo = fmt.Sprintf(" | boneco na tela: x=%d..%d y=%d..%d (centro %d,%d)",
				cli.X+r.L, cli.X+r.R, cli.Y+r.T, cli.Y+r.B,
				cli.X+(r.L+r.R)/2, cli.Y+(r.T+r.B)/2)
		}
		logMsg(fmt.Sprintf("hit rects: %d regiao(oes) | cliente origem=%d,%d%s", len(rs), cli.X, cli.Y, alvo))
	}
}

// SendChat — conversa de verdade com o Hermes.
//
// Híbrido de propósito:
//   - "saldo" e "status" (a palavra sozinha) respondem NA HORA, local (zero token);
//   - todo o resto vai para o agente completo (hermes -z, sessão contínua), o que
//     leva ~30 s e consome tokens da conta compartilhada.
func (a *App) SendChat(text string) string {
	return a.enviaChat("", text, nil)
}

// SendChatEm — o envio do chat com conversas e anexos (22/10/2026): `conversa` é o id
// guardado da conversa aberta na tela ("" = a atual da ponte) e `idsJSON` os anexos já
// preparados por AnexoPreparar. Devolve {resposta, conversa}: uma conversa nova ganha o id
// só agora, e a tela precisa dele.
func (a *App) SendChatEm(conversa, texto, idsJSON string) string {
	var ids []string
	_ = json.Unmarshal([]byte(idsJSON), &ids)
	anexos, err := pegaAnexos(ids)
	if err != nil {
		return respostaJSON(map[string]any{"resposta": "A mensagem não foi enviada. " + err.Error(), "conversa": conversa})
	}
	resp := a.enviaChat(conversa, texto, anexos)
	return respostaJSON(map[string]any{"resposta": resp, "conversa": conversaAtual()})
}

func (a *App) enviaChat(conversa, text string, anexos []map[string]any) string {
	cmd := comandoLocal(text)
	if len(anexos) > 0 {
		cmd = "" // com anexo, "saldo" é pergunta sobre o arquivo — vai para o agente
	} else if r, ok := tarefaDoChat(text); ok {
		a.avisaMudancaTarefas()
		return r
	}
	switch cmd {
	case "saldo":
		a.warmBalance()
		a.mu.Lock()
		b := a.balance
		a.mu.Unlock()
		return "Saldo da conta compartilhada: " + saldoSemRotulo(b) + "."
	case "status":
		age := heartbeatAge()
		if age < 0 {
			return "Não encontrei o sinal (heartbeat) do gateway."
		}
		if age < 120 {
			return fmt.Sprintf("O gateway está funcionando (último sinal há %d s). Sigo acompanhando.", age)
		}
		return fmt.Sprintf("Atenção: o gateway está sem sinal há %d min.", age/60)
	}
	resp := a.chamarHermes(conversa, text, anexos)
	// a resposta gastou crédito: relê o saldo (no máximo 1x/min) para o "gasto de hoje"
	// do cabeçalho do chat acompanhar — o pet-state leva o valor novo em até 15 s
	a.mu.Lock()
	velho := time.Since(a.balanceAt) > time.Minute
	a.mu.Unlock()
	if velho {
		go a.warmBalance()
	}
	return resp
}

// comandoLocal — "saldo" | "status" quando a mensagem é respondida aqui; "" = agente.
//
// Só a palavra SOZINHA é comando ("saldo", "status", "gateway", com ou sem "?").
// Antes bastava CONTER a palavra: "qual o saldo do projeto?" ou "configure o gateway
// do Telegram" devolviam o saldo/heartbeat e a pergunta nunca chegava ao agente.
func comandoLocal(texto string) string {
	switch strings.Trim(strings.ToLower(texto), " \t\r\n?!.") {
	case "saldo":
		return "saldo"
	case "status", "gateway":
		return "status"
	}
	return ""
}

// SavePet / LoadPet — posição e estado do boneco (pet como dado).
func petFile() string {
	lad := os.Getenv("LOCALAPPDATA")
	d := filepath.Join(lad, "petagent")
	os.MkdirAll(d, 0o755)
	return filepath.Join(d, "pets.json")
}

func (a *App) SavePet(js string) string {
	if err := os.WriteFile(petFile(), []byte(js), 0o644); err != nil {
		return "erro: " + err.Error()
	}
	return "ok"
}

func (a *App) LoadPet() string {
	b, err := os.ReadFile(petFile())
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (a *App) HideAll() {
	a.mu.Lock()
	a.hidden = true
	a.mu.Unlock()
	runtime.WindowHide(a.ctx)
}

func (a *App) ShowAll() {
	a.mu.Lock()
	a.hidden = false
	a.mu.Unlock()
	runtime.WindowShow(a.ctx)
}

// EsconderPor — esconde a JANELA por N minutos e traz de volta sozinha.
//
// Pedido do dono (03/10/2026) e o motivo: os itens "Esconder 15min / 1 hora" só
// punham uma classe CSS no boneco — a janela, que cobre a tela toda, CONTINUAVA no
// Windows (invisível, mas presente) e atrapalhava qualquer software que capture ou
// grave UMA janela específica, não só a Ferramenta de Captura. Com a janela
// escondida de verdade, o Windows deixa de vê-la e a captura de qualquer outra
// janela funciona — sem depender de monitorar qual ferramenta está aberta.
//
// O relógio é do lado Go de propósito: com a janela escondida, os timers do WebView2
// podem ser estrangulados e o boneco não voltaria na hora certa.
func (a *App) EsconderPor(minutos int) string {
	if minutos <= 0 {
		minutos = 15
	}
	if minutos > 24*60 {
		minutos = 24 * 60
	}
	a.mu.Lock()
	a.hidden = true
	// ⚠️ BUG MEDIDO (03/10/2026): cada clique criava um timer próprio, e o MAIS
	// ANTIGO disparava depois — reexibindo a janela no meio da captura do dono
	// (log: 13:21 escondida 15min + 13:35 escondida 15min → 13:36 "de volta").
	// Agora só o esconder MAIS NOVO manda: os anteriores são cancelados por geração.
	a.hideSeq++
	seq := a.hideSeq
	a.mu.Unlock()
	if a.ctx != nil {
		runtime.WindowHide(a.ctx)
	}
	logMsg(fmt.Sprintf("esconder: janela escondida por %d min (capturas liberadas)", minutos))
	time.AfterFunc(time.Duration(minutos)*time.Minute, func() {
		a.mu.Lock()
		if a.hideSeq != seq { // um esconder mais novo assumiu: este não manda mais
			a.mu.Unlock()
			logMsg("esconder: timer antigo descartado (havia um esconder mais novo)")
			return
		}
		a.hidden = false
		a.mu.Unlock()
		if a.ctx != nil {
			runtime.WindowShow(a.ctx)
			runtime.EventsEmit(a.ctx, "volta-escondido", minutos)
		}
		logMsg("esconder: tempo acabou, janela de volta")
	})
	return fmt.Sprintf("escondido por %d min", minutos)
}

func (a *App) ShowStatus() {
	age := heartbeatAge()
	a.warmBalance()
	a.mu.Lock()
	b := a.balance
	a.mu.Unlock()
	gw := fmt.Sprintf("último sinal há %d s", age)
	if age < 0 {
		gw = "sem sinal (heartbeat não encontrado)"
	}
	msg := fmt.Sprintf("Status do PetAgent\nGateway: %s\nSaldo: %s\nBonecos: 1 (Hermes)", gw, saldoSemRotulo(b))
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status-panel", msg)
	}
}

func (a *App) QuitApp() {
	systray.Quit()
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
	os.Exit(0)
}

// LogMsg — log em arquivo (o chat é para o veredito, não para log).
func (a *App) LogMsg(m string) string { logMsg(m); return "ok" }

func logMsg(m string) {
	lad := os.Getenv("LOCALAPPDATA")
	d := filepath.Join(lad, "petagent")
	os.MkdirAll(d, 0o755)
	f, err := os.OpenFile(filepath.Join(d, "petagent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("02/01 15:04:05"), m)
}

// (usa exec em etapa futura para abrir o dashboard; mantido p/ evitar import órfão)
var _ = exec.Command
