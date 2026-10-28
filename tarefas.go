package main

// Tarefas do dia (27/10/2026) — pedido da dona: "estou numa reunião e alguém me
// manda: Camila, tem que atualizar a conta do PayPal. Preciso mandar isso rápido para o
// boneco, ele monta a minha lista do dia e me lembra antes da hora."
//
// Aqui mora tudo o que é dado e regra: a lista (tarefas.json), as cadências
// (tarefas_config.json), a repetição e o cálculo de QUAL lembrete vence agora. O relógio
// e os avisos estão em tarefas_avisos.go; o entendimento do texto em tarefas_texto.go.
// O relógio é do lado Go de propósito (como o EsconderPor): com a janela escondida os
// timers do WebView2 são estrangulados e o lembrete chegaria atrasado.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	fmtDia      = "2006-01-02"
	fmtHora     = "15:04"
	fmtMomento  = "2006-01-02T15:04:05"
	maxAvisados = 40
	// tarefa feita há mais que isto sai do arquivo (a lista não cresce para sempre)
	guardaFeitas = 180 * 24 * time.Hour
)

// RepeteTarefa — "" (não repete) | diaria | uteis | semanal (Dias: 0=dom…6=sáb) | mensal (DiaMes).
type RepeteTarefa struct {
	Tipo   string `json:"tipo"`
	Dias   []int  `json:"dias,omitempty"`
	DiaMes int    `json:"dia_mes,omitempty"`
}

type Tarefa struct {
	ID     string `json:"id"`
	Titulo string `json:"titulo"`
	Nota   string `json:"nota,omitempty"`
	Criada string `json:"criada"`
	Data   string `json:"data"` // AAAA-MM-DD; "" = sem data
	Hora   string `json:"hora"` // HH:MM; "" = sem horário
	// Lembretes — minutos antes do prazo. null = o padrão das configurações; [] = nenhum.
	// Sem omitempty de propósito: é o null/[] do JSON que separa os dois casos.
	Lembretes   []int        `json:"lembretes"`
	Repete      RepeteTarefa `json:"repete"`
	SemInsistir bool         `json:"sem_insistir,omitempty"`
	Feita       bool         `json:"feita"`
	FeitaEm     string       `json:"feita_em,omitempty"`
	AdiadaAte   string       `json:"adiada_ate,omitempty"`
	Origem      string       `json:"origem,omitempty"`  // texto | audio | chat
	Proxima     string       `json:"proxima,omitempty"` // id da ocorrência seguinte (repetição)
	// controle dos avisos: o que já disparou (sobrevive a reiniciar o app) e quando foi o último
	Avisados    []string `json:"avisados,omitempty"`
	UltimoAviso string   `json:"ultimo_aviso,omitempty"`
}

// ConfigTarefas — as cadências (painel Tarefas › ajustes).
type ConfigTarefas struct {
	Lembretes      []int  `json:"lembretes"`        // antecedência padrão (min)
	HoraSemHorario string `json:"hora_sem_horario"` // tarefa só com data: lembra a esta hora
	InsistirMin    int    `json:"insistir_min"`     // atrasada: lembra de novo a cada N min (0 = não)
	InsistirAte    string `json:"insistir_ate"`     // e para de insistir a esta hora
	PausaFimSemana bool   `json:"pausa_fim_semana"` // não insiste sábado e domingo
	Windows        bool   `json:"windows"`          // notificação do Windows junto com o balão
	PerguntarPrazo bool   `json:"perguntar_prazo"`  // ao anotar, o boneco pergunta o prazo
	ResumoManha    bool   `json:"resumo_manha"`     // ao começar o dia, mostra as tarefas de hoje
	Atalho         string `json:"atalho"`           // ctrl+alt+t | ctrl+alt+n | ctrl+shift+espaco
	UltimoResumo   string `json:"ultimo_resumo,omitempty"`
}

func configTarefasPadrao() ConfigTarefas {
	return ConfigTarefas{
		Lembretes: []int{0, 15, 60}, HoraSemHorario: "09:00", InsistirMin: 30, InsistirAte: "20:00",
		PausaFimSemana: true, Windows: true, PerguntarPrazo: true, ResumoManha: true, Atalho: "ctrl+alt+t",
	}
}

// ── arquivos ──────────────────────────────────────────────────────────

var tarefasMu sync.Mutex

// variáveis para o teste apontar para uma pasta temporária
var (
	arquivoTarefas       = func() string { return filepath.Join(pastaPetAgente(), "tarefas.json") }
	arquivoConfigTarefas = func() string { return filepath.Join(pastaPetAgente(), "tarefas_config.json") }
)

func leTarefas() []Tarefa {
	var d struct {
		Tarefas []Tarefa `json:"tarefas"`
	}
	if b, err := os.ReadFile(arquivoTarefas()); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	return d.Tarefas
}

func gravaArquivoJSON(caminho string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := caminho + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

func gravaTarefas(ts []Tarefa, agora time.Time) error {
	fica := ts[:0:0]
	for _, t := range ts {
		if t.Feita && t.FeitaEm != "" {
			if f, err := time.ParseInLocation(fmtMomento, t.FeitaEm, time.Local); err == nil && agora.Sub(f) > guardaFeitas {
				continue
			}
		}
		fica = append(fica, t)
	}
	return gravaArquivoJSON(arquivoTarefas(), map[string]any{"tarefas": fica})
}

func leConfigTarefas() ConfigTarefas {
	c := configTarefasPadrao()
	if b, err := os.ReadFile(arquivoConfigTarefas()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Lembretes == nil {
		c.Lembretes = []int{}
	}
	if _, err := time.Parse(fmtHora, c.HoraSemHorario); err != nil {
		c.HoraSemHorario = "09:00"
	}
	if _, err := time.Parse(fmtHora, c.InsistirAte); err != nil {
		c.InsistirAte = "20:00"
	}
	if c.InsistirMin < 0 {
		c.InsistirMin = 0
	}
	if _, ok := atalhosTarefa[c.Atalho]; !ok {
		c.Atalho = "ctrl+alt+t"
	}
	return c
}

func novoIDTarefa() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "t" + time.Now().Format("060102150405") + hex.EncodeToString(b)
}

// ── normalização (o que vem da tela) ─────────────────────────────────

func limpaLembretes(ls []int) []int {
	if ls == nil {
		return nil
	}
	vistos := map[int]bool{}
	out := []int{}
	for _, m := range ls {
		if m < 0 || m > 7*24*60 || vistos[m] {
			continue
		}
		vistos[m] = true
		out = append(out, m)
	}
	sort.Ints(out)
	return out
}

// normaliza — corta espaço, valida data/hora e dá uma data à tarefa que se repete.
func normaliza(t *Tarefa, agora time.Time) error {
	t.Titulo = strings.TrimSpace(t.Titulo)
	t.Nota = strings.TrimSpace(t.Nota)
	if t.Titulo == "" {
		return errors.New("escreva o que precisa ser feito")
	}
	if len([]rune(t.Titulo)) > 300 {
		t.Titulo = string([]rune(t.Titulo)[:300])
	}
	if t.Data != "" {
		if _, err := time.ParseInLocation(fmtDia, t.Data, time.Local); err != nil {
			return errors.New("a data não está no formato certo")
		}
	}
	if t.Hora != "" {
		if _, err := time.Parse(fmtHora, t.Hora); err != nil {
			return errors.New("o horário não está no formato certo")
		}
	}
	if t.Data == "" {
		t.Hora = "" // horário sem dia não tem como lembrar
	}
	t.Lembretes = limpaLembretes(t.Lembretes)
	switch t.Repete.Tipo {
	case "", "diaria", "uteis", "semanal", "mensal":
	default:
		t.Repete.Tipo = ""
	}
	if t.Repete.Tipo == "" {
		t.Repete = RepeteTarefa{}
		return nil
	}
	base := agora
	if t.Data != "" {
		base, _ = time.ParseInLocation(fmtDia, t.Data, time.Local)
	}
	if t.Repete.Tipo == "semanal" {
		dias := []int{}
		for _, x := range t.Repete.Dias {
			if x >= 0 && x <= 6 {
				dias = append(dias, x)
			}
		}
		if len(dias) == 0 {
			dias = []int{int(base.Weekday())}
		}
		t.Repete.Dias = dias
	} else {
		t.Repete.Dias = nil
	}
	if t.Repete.Tipo == "mensal" {
		if t.Repete.DiaMes < 1 || t.Repete.DiaMes > 31 {
			t.Repete.DiaMes = base.Day()
		}
	} else {
		t.Repete.DiaMes = 0
	}
	if t.Data == "" { // repete sem data: a primeira ocorrência a partir de hoje
		ontem := agora.AddDate(0, 0, -1).Format(fmtDia)
		t.Data = proximaData(t.Repete, ontem, agora.Format(fmtDia))
	}
	return nil
}

// ── repetição ────────────────────────────────────────────────────────

// proximaData — o primeiro dia DEPOIS de `depois` que cai na regra (e não antes de
// `minimo`, quando dado). Mensal no dia 31 cai no último dia dos meses mais curtos.
func proximaData(r RepeteTarefa, depois, minimo string) string {
	d, err := time.ParseInLocation(fmtDia, depois, time.Local)
	if err != nil {
		return ""
	}
	min, _ := time.ParseInLocation(fmtDia, minimo, time.Local)
	for i := 0; i < 800; i++ {
		d = d.AddDate(0, 0, 1)
		if !min.IsZero() && d.Before(min) {
			continue
		}
		wd := int(d.Weekday())
		switch r.Tipo {
		case "diaria":
			return d.Format(fmtDia)
		case "uteis":
			if wd != 0 && wd != 6 {
				return d.Format(fmtDia)
			}
		case "semanal":
			dias := r.Dias
			if len(dias) == 0 {
				dias = []int{int(d.AddDate(0, 0, -i-1).Weekday())}
			}
			for _, x := range dias {
				if x == wd {
					return d.Format(fmtDia)
				}
			}
		case "mensal":
			alvo := r.DiaMes
			ultimo := time.Date(d.Year(), d.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
			if alvo > ultimo {
				alvo = ultimo
			}
			if d.Day() == alvo {
				return d.Format(fmtDia)
			}
		default:
			return ""
		}
	}
	return ""
}

// ── qual lembrete vence agora ────────────────────────────────────────

type avisoTarefa struct {
	Tipo    string `json:"tipo"`    // antes | hora | atrasada | adiada
	Minutos int    `json:"minutos"` // antes: faltam N min; atrasada: passou N min
}

// prazoDe — o momento de entrega. Sem horário, conta o horário de início do dia.
func prazoDe(t Tarefa, c ConfigTarefas) (time.Time, bool) {
	if t.Data == "" {
		return time.Time{}, false
	}
	h := t.Hora
	if h == "" {
		h = c.HoraSemHorario
	}
	p, err := time.ParseInLocation(fmtDia+" "+fmtHora, t.Data+" "+h, time.Local)
	return p, err == nil
}

func (t *Tarefa) jaAvisou(chave string) bool {
	for _, k := range t.Avisados {
		if k == chave {
			return true
		}
	}
	return false
}

func (t *Tarefa) marcaAviso(chave string) {
	t.Avisados = append(t.Avisados, chave)
	if len(t.Avisados) > maxAvisados {
		t.Avisados = t.Avisados[len(t.Avisados)-maxAvisados:]
	}
}

// dentroDaJanela — a insistência só acontece no horário de trabalho configurado.
func dentroDaJanela(agora time.Time, c ConfigTarefas) bool {
	if c.PausaFimSemana && (agora.Weekday() == time.Saturday || agora.Weekday() == time.Sunday) {
		return false
	}
	hm := agora.Format(fmtHora)
	return hm >= c.HoraSemHorario && hm <= c.InsistirAte
}

func minutosEntre(a, b time.Time) int {
	d := b.Sub(a)
	if d < 0 {
		d = -d
	}
	return int((d + time.Minute - 1) / time.Minute)
}

// avisoDevido — o lembrete que vence AGORA para esta tarefa (no máximo um por vez) e
// já o marca como dado. Regras:
//   - antecedências: cada uma dispara uma vez por prazo (a chave leva o prazo: mudar a
//     data rearma tudo). Se várias venceram juntas (o PC estava desligado), só a mais
//     perto da entrega vira aviso;
//   - adiada: nada até a hora do adiamento; aí um aviso;
//   - atrasada: de novo a cada InsistirMin, só dentro da janela de trabalho.
func avisoDevido(t *Tarefa, c ConfigTarefas, agora time.Time) (avisoTarefa, bool) {
	if t.Feita {
		return avisoTarefa{}, false
	}
	prazo, ok := prazoDe(*t, c)
	if !ok {
		return avisoTarefa{}, false
	}
	dar := func(a avisoTarefa) (avisoTarefa, bool) {
		t.UltimoAviso = agora.Format(fmtMomento)
		return a, true
	}
	if t.AdiadaAte != "" {
		if ate, err := time.ParseInLocation(fmtMomento, t.AdiadaAte, time.Local); err == nil {
			if agora.Before(ate) {
				return avisoTarefa{}, false
			}
			if k := "adiada@" + t.AdiadaAte; !t.jaAvisou(k) {
				t.marcaAviso(k)
				for _, m := range listaLembretes(*t, c) { // o que venceu durante o adiamento não volta
					if !agora.Before(prazo.Add(-time.Duration(m) * time.Minute)) {
						if k2 := chaveLembrete(m, prazo); !t.jaAvisou(k2) {
							t.marcaAviso(k2)
						}
					}
				}
				return dar(avisoTarefa{Tipo: "adiada"})
			}
		}
	}

	menor := -1
	for _, m := range listaLembretes(*t, c) {
		if agora.Before(prazo.Add(-time.Duration(m) * time.Minute)) {
			continue
		}
		k := chaveLembrete(m, prazo)
		if t.jaAvisou(k) {
			continue
		}
		t.marcaAviso(k)
		if menor < 0 || m < menor {
			menor = m
		}
	}
	if menor >= 0 {
		switch {
		case agora.Before(prazo):
			return dar(avisoTarefa{Tipo: "antes", Minutos: minutosEntre(agora, prazo)})
		case agora.Sub(prazo) <= 2*time.Minute:
			return dar(avisoTarefa{Tipo: "hora"})
		default:
			return dar(avisoTarefa{Tipo: "atrasada", Minutos: minutosEntre(prazo, agora)})
		}
	}

	if !agora.After(prazo) || c.InsistirMin <= 0 || t.SemInsistir || !dentroDaJanela(agora, c) {
		return avisoTarefa{}, false
	}
	if t.UltimoAviso != "" {
		if u, err := time.ParseInLocation(fmtMomento, t.UltimoAviso, time.Local); err == nil &&
			agora.Sub(u) < time.Duration(c.InsistirMin)*time.Minute {
			return avisoTarefa{}, false
		}
	}
	return dar(avisoTarefa{Tipo: "atrasada", Minutos: minutosEntre(prazo, agora)})
}

func chaveLembrete(m int, prazo time.Time) string {
	return fmt.Sprintf("m%d@%s", m, prazo.Format(fmtDia+" "+fmtHora))
}

// listaLembretes — os da tarefa ou o padrão. Sem horário, "15 min antes das 9:00" não
// faz sentido: vale só o da hora e os de 1 dia ou mais.
func listaLembretes(t Tarefa, c ConfigTarefas) []int {
	ls := t.Lembretes
	if ls == nil {
		ls = c.Lembretes
	}
	if t.Hora != "" {
		return ls
	}
	out := []int{}
	tem0 := false
	for _, m := range ls {
		if m >= 24*60 {
			out = append(out, m)
		}
		if m == 0 {
			tem0 = true
		}
	}
	if tem0 || len(ls) > 0 {
		out = append(out, 0)
	}
	return out
}

// ── operações (usadas pelos bindings, pelo chat e pela notificação) ──

// mudaTarefa — lê, aplica `f` na tarefa `id` e grava, tudo sob o mutex.
func mudaTarefa(id string, f func(ts []Tarefa, i int, agora time.Time) ([]Tarefa, error)) (Tarefa, error) {
	tarefasMu.Lock()
	defer tarefasMu.Unlock()
	ts := leTarefas()
	for i := range ts {
		if ts[i].ID == id {
			agora := time.Now()
			novo, err := f(ts, i, agora)
			if err != nil {
				return Tarefa{}, err
			}
			var t Tarefa
			for _, x := range novo {
				if x.ID == id {
					t = x
				}
			}
			return t, gravaTarefas(novo, agora)
		}
	}
	return Tarefa{}, errors.New("essa tarefa não existe mais")
}

func criaTarefa(t Tarefa, agora time.Time) (Tarefa, error) {
	if err := normaliza(&t, agora); err != nil {
		return Tarefa{}, err
	}
	t.ID = novoIDTarefa()
	t.Criada = agora.Format(fmtMomento)
	t.Feita, t.FeitaEm, t.AdiadaAte, t.Proxima = false, "", "", ""
	t.Avisados, t.UltimoAviso = nil, ""
	tarefasMu.Lock()
	defer tarefasMu.Unlock()
	ts := append(leTarefas(), t)
	return t, gravaTarefas(ts, agora)
}

// concluiTarefa — marca (ou desmarca). Feita e com repetição: nasce a próxima ocorrência,
// uma vez só (desmarcar e marcar de novo não cria outra).
func concluiTarefa(id string, feita bool) (Tarefa, error) {
	return mudaTarefa(id, func(ts []Tarefa, i int, agora time.Time) ([]Tarefa, error) {
		t := &ts[i]
		t.Feita = feita
		t.FeitaEm = ""
		if feita {
			t.FeitaEm = agora.Format(fmtMomento)
			t.AdiadaAte = ""
		}
		if feita && t.Repete.Tipo != "" && t.Proxima == "" {
			prox := *t
			prox.ID = novoIDTarefa()
			prox.Criada = agora.Format(fmtMomento)
			prox.Data = proximaData(t.Repete, t.Data, agora.Format(fmtDia))
			prox.Feita, prox.FeitaEm, prox.Proxima, prox.AdiadaAte = false, "", "", ""
			prox.Avisados, prox.UltimoAviso = nil, ""
			if prox.Data != "" {
				t.Proxima = prox.ID
				ts = append(ts, prox)
			}
		}
		return ts, nil
	})
}

// adiaTarefa — minutos > 0: adia; -1: passa para amanhã (mesmo horário).
func adiaTarefa(id string, minutos int) (Tarefa, error) {
	return mudaTarefa(id, func(ts []Tarefa, i int, agora time.Time) ([]Tarefa, error) {
		t := &ts[i]
		if minutos == -1 {
			t.Data = agora.AddDate(0, 0, 1).Format(fmtDia)
			t.AdiadaAte, t.UltimoAviso = "", ""
			return ts, nil
		}
		if minutos <= 0 || minutos > 7*24*60 {
			return nil, errors.New("tempo de adiamento inválido")
		}
		t.AdiadaAte = agora.Add(time.Duration(minutos) * time.Minute).Format(fmtMomento)
		return ts, nil
	})
}

// atualizaTarefa — a edição: o que é de controle (criada, avisos, repetição já gerada)
// fica como estava; mudar o prazo rearma o adiamento.
func atualizaTarefa(n Tarefa) (Tarefa, error) {
	return mudaTarefa(n.ID, func(ts []Tarefa, i int, agora time.Time) ([]Tarefa, error) {
		v := ts[i]
		if err := normaliza(&n, agora); err != nil {
			return nil, err
		}
		n.Criada, n.Feita, n.FeitaEm, n.Proxima = v.Criada, v.Feita, v.FeitaEm, v.Proxima
		n.Avisados, n.UltimoAviso, n.AdiadaAte = v.Avisados, v.UltimoAviso, v.AdiadaAte
		if n.Origem == "" {
			n.Origem = v.Origem
		}
		if n.Data != v.Data || n.Hora != v.Hora {
			n.AdiadaAte, n.UltimoAviso = "", ""
		}
		ts[i] = n
		return ts, nil
	})
}

func excluiTarefa(id string) error {
	tarefasMu.Lock()
	defer tarefasMu.Unlock()
	ts := leTarefas()
	for i := range ts {
		if ts[i].ID == id {
			return gravaTarefas(append(ts[:i:i], ts[i+1:]...), time.Now())
		}
	}
	return errors.New("essa tarefa não existe mais")
}

// ── bindings ─────────────────────────────────────────────────────────

func erroJSON(err error) string {
	return respostaJSON(map[string]any{"ok": false, "erro": err.Error()})
}

func (a *App) tarefaPronta(t Tarefa, err error) string {
	if err != nil {
		return erroJSON(err)
	}
	a.avisaMudancaTarefas()
	return respostaJSON(map[string]any{"ok": true, "tarefa": t})
}

// TarefasListar — todas (a lista é pequena: a tela filtra por dia, semana e mês).
func (a *App) TarefasListar() string {
	tarefasMu.Lock()
	ts := leTarefas()
	tarefasMu.Unlock()
	if ts == nil {
		ts = []Tarefa{}
	}
	return respostaJSON(map[string]any{"tarefas": ts, "hoje": time.Now().Format(fmtDia)})
}

func (a *App) TarefaCriar(js string) string {
	var t Tarefa
	if err := json.Unmarshal([]byte(js), &t); err != nil {
		return erroJSON(errors.New("dados da tarefa ilegíveis"))
	}
	return a.tarefaPronta(criaTarefa(t, time.Now()))
}

func (a *App) TarefaAtualizar(js string) string {
	var t Tarefa
	if err := json.Unmarshal([]byte(js), &t); err != nil {
		return erroJSON(errors.New("dados da tarefa ilegíveis"))
	}
	return a.tarefaPronta(atualizaTarefa(t))
}

func (a *App) TarefaConcluir(id string, feita bool) string {
	return a.tarefaPronta(concluiTarefa(id, feita))
}

func (a *App) TarefaAdiar(id string, minutos int) string {
	return a.tarefaPronta(adiaTarefa(id, minutos))
}

func (a *App) TarefaExcluir(id string) string {
	if err := excluiTarefa(id); err != nil {
		return erroJSON(err)
	}
	a.avisaMudancaTarefas()
	return respostaJSON(map[string]any{"ok": true})
}

func (a *App) TarefasConfig() string {
	c := leConfigTarefas()
	return respostaJSON(map[string]any{"config": c, "atalhos": opcoesAtalho(), "atalho_erro": atalhoErro()})
}

func (a *App) TarefasConfigSalvar(js string) string {
	var n ConfigTarefas
	if err := json.Unmarshal([]byte(js), &n); err != nil {
		return erroJSON(errors.New("configuração ilegível"))
	}
	tarefasMu.Lock()
	v := leConfigTarefas()
	n.UltimoResumo = v.UltimoResumo
	n.Lembretes = limpaLembretes(n.Lembretes)
	if n.Lembretes == nil {
		n.Lembretes = []int{}
	}
	err := gravaArquivoJSON(arquivoConfigTarefas(), n)
	tarefasMu.Unlock()
	if err != nil {
		return erroJSON(err)
	}
	c := leConfigTarefas()
	if c.Atalho != v.Atalho {
		a.trocaAtalhoTarefa()
	}
	return respostaJSON(map[string]any{"ok": true, "config": c, "atalho_erro": atalhoErro()})
}
