package main

// O relógio das tarefas: a cada 30 s vê qual lembrete venceu e avisa nos dois lugares —
// o balão do boneco (evento "tarefa-lembrete") e a notificação do Windows, que chega mesmo
// com o boneco escondido (ele some durante capturas e compartilhamento de tela).
// Também aqui: o atalho global de captura (Ctrl+Alt+T) e o "/tarefa" do chat.

import (
	"fmt"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ── textos do aviso (os mesmos no balão e no Windows) ────────────────

func duracaoTexto(min int) string {
	switch {
	case min < 60:
		return fmt.Sprintf("%d min", min)
	case min < 24*60:
		h, m := min/60, min%60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %02d", h, m)
	default:
		d := min / (24 * 60)
		if d == 1 {
			return "1 dia"
		}
		return fmt.Sprintf("%d dias", d)
	}
}

var nomesDia = []string{"dom", "seg", "ter", "qua", "qui", "sex", "sáb"}

// quandoTexto — "hoje às 10:00", "amanhã", "qua 05/11 às 9:00".
func quandoTexto(t Tarefa, agora time.Time) string {
	d, err := time.ParseInLocation(fmtDia, t.Data, time.Local)
	if err != nil {
		return "sem data"
	}
	var dia string
	switch t.Data {
	case agora.Format(fmtDia):
		dia = "hoje"
	case agora.AddDate(0, 0, 1).Format(fmtDia):
		dia = "amanhã"
	case agora.AddDate(0, 0, -1).Format(fmtDia):
		dia = "ontem"
	default:
		dia = nomesDia[d.Weekday()] + " " + d.Format("02/01")
	}
	if t.Hora == "" {
		return dia
	}
	return dia + " às " + t.Hora
}

func textoAviso(t Tarefa, av avisoTarefa, agora time.Time) (titulo, corpo string) {
	switch av.Tipo {
	case "antes":
		return "Em " + duracaoTexto(av.Minutos) + ": " + t.Titulo, "Entrega " + quandoTexto(t, agora) + "."
	case "hora":
		return "Agora: " + t.Titulo, "Está na hora da entrega (" + quandoTexto(t, agora) + ")."
	case "adiada":
		return "Lembrete: " + t.Titulo, "O adiamento terminou. Entrega " + quandoTexto(t, agora) + "."
	default:
		return "Atrasada: " + t.Titulo, "O prazo passou há " + duracaoTexto(av.Minutos) + " (" + quandoTexto(t, agora) + ")."
	}
}

// ── o laço ───────────────────────────────────────────────────────────

func (a *App) avisaMudancaTarefas() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "tarefas-mudou")
	}
}

func (a *App) tarefasLoop() {
	time.Sleep(10 * time.Second) // deixa a janela e os bindings subirem primeiro
	a.iniciaNotificacoes()
	for {
		a.verificaTarefas(time.Now())
		time.Sleep(30 * time.Second)
	}
}

type lembreteTarefa struct {
	Tarefa Tarefa      `json:"tarefa"`
	Aviso  avisoTarefa `json:"aviso"`
	Titulo string      `json:"titulo"`
	Texto  string      `json:"texto"`
}

// lembretesDaVez — o cálculo do laço, sem tela: o que avisar agora e o resumo da manhã.
func lembretesDaVez(agora time.Time) (avs []lembreteTarefa, resumo map[string]any, cfg ConfigTarefas) {
	tarefasMu.Lock()
	defer tarefasMu.Unlock()
	c := leConfigTarefas()
	ts := leTarefas()
	mudou := false
	for i := range ts {
		antes := len(ts[i].Avisados)
		if av, ok := avisoDevido(&ts[i], c, agora); ok {
			tit, txt := textoAviso(ts[i], av, agora)
			avs = append(avs, lembreteTarefa{ts[i], av, tit, txt})
			mudou = true
		} else if len(ts[i].Avisados) != antes {
			mudou = true
		}
	}
	if mudou {
		if err := gravaTarefas(ts, agora); err != nil {
			logMsg("tarefas: nao consegui gravar os avisos: " + err.Error())
		}
	}
	hoje := agora.Format(fmtDia)
	fimDeSemana := agora.Weekday() == time.Saturday || agora.Weekday() == time.Sunday
	if c.ResumoManha && c.UltimoResumo != hoje && agora.Format(fmtHora) >= c.HoraSemHorario &&
		!(c.PausaFimSemana && fimDeSemana) {
		pend, atras := 0, 0
		primeira := ""
		for _, t := range ts {
			if t.Feita || t.Data == "" {
				continue
			}
			if t.Data == hoje {
				pend++
				if t.Hora != "" && (primeira == "" || t.Hora < primeira[:5]) {
					primeira = t.Hora + " " + t.Titulo
				}
			} else if t.Data < hoje {
				atras++
			}
		}
		c.UltimoResumo = hoje
		_ = gravaArquivoJSON(arquivoConfigTarefas(), c)
		if pend+atras > 0 {
			resumo = map[string]any{"hoje": pend, "atrasadas": atras, "primeira": primeira}
		}
	}
	return avs, resumo, c
}

func (a *App) verificaTarefas(agora time.Time) {
	avs, resumo, c := lembretesDaVez(agora)
	if a.ctx == nil {
		return
	}
	for _, l := range avs {
		logMsg("tarefas: lembrete " + l.Aviso.Tipo + " | " + l.Tarefa.Titulo)
		runtime.EventsEmit(a.ctx, "tarefa-lembrete", l)
		if c.Windows {
			a.notificaWindows(l)
		}
	}
	if resumo != nil {
		runtime.EventsEmit(a.ctx, "tarefas-resumo", resumo)
	}
	if len(avs) > 0 || resumo != nil {
		a.avisaMudancaTarefas()
	}
}

// ── notificação do Windows (a do próprio Wails, com botões) ──────────

var notifPronta atomic.Bool

func (a *App) iniciaNotificacoes() {
	if a.ctx == nil {
		return
	}
	if err := runtime.InitializeNotifications(a.ctx); err != nil {
		logMsg("tarefas: notificacoes do Windows indisponiveis: " + err.Error())
		return
	}
	err := runtime.RegisterNotificationCategory(a.ctx, runtime.NotificationCategory{
		ID: "tarefa",
		Actions: []runtime.NotificationAction{
			{ID: "feito", Title: "Feito"},
			{ID: "adiar10", Title: "Adiar 10 min"},
		},
	})
	if err != nil {
		logMsg("tarefas: botoes da notificacao indisponiveis: " + err.Error())
	}
	runtime.OnNotificationResponse(a.ctx, a.respostaNotificacao)
	notifPronta.Store(true)
}

func (a *App) notificaWindows(l lembreteTarefa) {
	if !notifPronta.Load() {
		return
	}
	err := runtime.SendNotificationWithActions(a.ctx, runtime.NotificationOptions{
		ID:         "tarefa-" + l.Tarefa.ID + "-" + time.Now().Format("150405"),
		Title:      l.Titulo,
		Body:       l.Texto,
		CategoryID: "tarefa",
		Data:       map[string]any{"tarefa": l.Tarefa.ID},
	})
	if err != nil {
		logMsg("tarefas: notificacao do Windows falhou: " + err.Error())
	}
}

// respostaNotificacao — clicou em "Feito", "Adiar" ou no corpo (abre o painel na tarefa).
func (a *App) respostaNotificacao(r runtime.NotificationResult) {
	if r.Error != nil {
		logMsg("tarefas: resposta da notificacao ilegivel: " + r.Error.Error())
		return
	}
	id, _ := r.Response.UserInfo["tarefa"].(string)
	if id == "" {
		return
	}
	var err error
	switch r.Response.ActionIdentifier {
	case "feito":
		_, err = concluiTarefa(id, true)
	case "adiar10":
		_, err = adiaTarefa(id, 10)
	default:
		a.mostraJanela()
		runtime.EventsEmit(a.ctx, "tarefa-abrir", id)
		return
	}
	if err != nil {
		logMsg("tarefas: acao da notificacao falhou: " + err.Error())
	}
	a.avisaMudancaTarefas()
}

// mostraJanela — traz o boneco de volta (estava escondido) e para a frente.
func (a *App) mostraJanela() {
	a.mu.Lock()
	escondido := a.hidden
	hw := a.hwnd
	a.mu.Unlock()
	if escondido {
		a.ShowAll()
	}
	if hw != 0 {
		procSetForegroundWindow.Call(uintptr(hw))
	}
}

// ── atalho global de captura ─────────────────────────────────────────
// RegisterHotKey (e não GetAsyncKeyState, como o Ctrl+Alt+Q): o Windows entrega o
// atalho só para nós — ele não vaza para o programa em primeiro plano — e quem recebe
// WM_HOTKEY pode trazer a própria janela para a frente, para a pessoa já sair digitando.

var (
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCurrentThreadId  = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
)

const (
	modAlt, modCtrl, modShift, modNoRepeat = 0x1, 0x2, 0x4, 0x4000
	wmHotkey                               = 0x0312
	wmTrocaAtalho                          = 0x8001 // WM_APP+1
)

type atalhoTarefa struct {
	mods, vk uintptr
	nome     string
}

var atalhosTarefa = map[string]atalhoTarefa{
	"ctrl+alt+t":        {modCtrl | modAlt, 'T', "Ctrl + Alt + T"},
	"ctrl+alt+n":        {modCtrl | modAlt, 'N', "Ctrl + Alt + N"},
	"ctrl+shift+espaco": {modCtrl | modShift, 0x20, "Ctrl + Shift + Espaço"},
}

func opcoesAtalho() []map[string]string {
	out := []map[string]string{}
	for _, id := range []string{"ctrl+alt+t", "ctrl+alt+n", "ctrl+shift+espaco"} {
		out = append(out, map[string]string{"id": id, "nome": atalhosTarefa[id].nome})
	}
	return out
}

var (
	atalhoTid    atomic.Uint32
	atalhoErroMu sync.Mutex
	atalhoErroTx string
)

func atalhoErro() string {
	atalhoErroMu.Lock()
	defer atalhoErroMu.Unlock()
	return atalhoErroTx
}

type msgWin struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Privado uint32
}

func (a *App) atalhoTarefas() {
	goruntime.LockOSThread() // o atalho pertence à thread que o registrou: ela é só dele
	tid, _, _ := procGetCurrentThreadId.Call()
	atalhoTid.Store(uint32(tid))
	registra := func() {
		procUnregisterHotKey.Call(0, 1)
		at := atalhosTarefa[leConfigTarefas().Atalho]
		ok, _, _ := procRegisterHotKey.Call(0, 1, at.mods|modNoRepeat, at.vk)
		atalhoErroMu.Lock()
		if ok == 0 {
			atalhoErroTx = "O atalho " + at.nome + " já está em uso por outro programa. Escolha outro."
			logMsg("tarefas: atalho " + at.nome + " em uso por outro programa")
		} else {
			atalhoErroTx = ""
			logMsg("tarefas: atalho " + at.nome + " registrado")
		}
		atalhoErroMu.Unlock()
	}
	registra()
	var m msgWin
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		switch m.Message {
		case wmHotkey:
			a.capturaTarefa()
		case wmTrocaAtalho:
			registra()
		}
	}
}

func (a *App) trocaAtalhoTarefa() {
	if tid := atalhoTid.Load(); tid != 0 {
		procPostThreadMessageW.Call(uintptr(tid), wmTrocaAtalho, 0, 0)
		time.Sleep(150 * time.Millisecond) // a resposta do salvar já leva o erro, se houver
	}
}

func (a *App) capturaTarefa() {
	logMsg("tarefas: atalho de captura")
	a.mostraJanela()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "captura-tarefa")
	}
}

// ── "/tarefa" no chat ────────────────────────────────────────────────

// tarefaDoChat — "/tarefa pagar o boleto dia 5" cria a tarefa aqui mesmo, sem gastar o
// agente. ok=false: não era o comando (a mensagem segue o caminho de sempre).
func tarefaDoChat(texto string) (resposta string, ok bool) {
	t := strings.TrimSpace(texto)
	low := strings.ToLower(t)
	if low != "/tarefa" && !strings.HasPrefix(low, "/tarefa ") {
		return "", false
	}
	resto := strings.TrimSpace(t[len("/tarefa"):])
	if resto == "" {
		return "Escreva a tarefa depois do comando. Por exemplo: /tarefa pagar o boleto do fornecedor dia 5", true
	}
	agora := time.Now()
	e := entendeTarefa(resto, agora)
	nova, err := criaTarefa(Tarefa{Titulo: e.Titulo, Data: e.Data, Hora: e.Hora, Repete: e.Repete, Origem: "chat"}, agora)
	if err != nil {
		return "Não consegui anotar a tarefa: " + err.Error() + ".", true
	}
	return "Tarefa anotada: " + nova.Titulo + " (" + quandoTexto(nova, agora) + "). Ela está no painel Tarefas.", true
}
