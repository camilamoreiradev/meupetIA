package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// terça-feira, 27/10/2026
func ter(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, time.Local) }

// pastaDeTeste — tarefas.json e tarefas_config.json numa pasta temporária.
func pastaDeTeste(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	at, ac := arquivoTarefas, arquivoConfigTarefas
	arquivoTarefas = func() string { return filepath.Join(d, "tarefas.json") }
	arquivoConfigTarefas = func() string { return filepath.Join(d, "tarefas_config.json") }
	t.Cleanup(func() { arquivoTarefas, arquivoConfigTarefas = at, ac })
}

func TestProximaData(t *testing.T) {
	casos := []struct {
		nome   string
		r      RepeteTarefa
		depois string
		quer   string
	}{
		{"todo dia", RepeteTarefa{Tipo: "diaria"}, "2026-10-27", "2026-10-28"},
		{"dias úteis pula o fim de semana", RepeteTarefa{Tipo: "uteis"}, "2026-10-30", "2026-11-02"},
		{"semanal terça e quinta", RepeteTarefa{Tipo: "semanal", Dias: []int{2, 4}}, "2026-10-27", "2026-10-29"},
		{"semanal volta para a terça", RepeteTarefa{Tipo: "semanal", Dias: []int{2, 4}}, "2026-10-29", "2026-11-03"},
		{"mensal dia 31 em fevereiro", RepeteTarefa{Tipo: "mensal", DiaMes: 31}, "2026-01-31", "2026-02-28"},
		{"mensal dia 31 volta ao 31", RepeteTarefa{Tipo: "mensal", DiaMes: 31}, "2026-02-28", "2026-03-31"},
		{"não repete", RepeteTarefa{}, "2026-10-27", ""},
	}
	for _, c := range casos {
		if got := proximaData(c.r, c.depois, ""); got != c.quer {
			t.Errorf("%s: proximaData = %q, quer %q", c.nome, got, c.quer)
		}
	}
	// o mínimo: concluída com atraso, a próxima não cai no passado
	if got := proximaData(RepeteTarefa{Tipo: "diaria"}, "2026-10-20", "2026-10-27"); got != "2026-10-27" {
		t.Errorf("com mínimo: %q, quer 2026-10-27", got)
	}
}

func TestNormalizaRepeticaoSemData(t *testing.T) {
	tf := Tarefa{Titulo: "  relatório  ", Repete: RepeteTarefa{Tipo: "semanal", Dias: []int{4, 9}}}
	if err := normaliza(&tf, ter(9, 0)); err != nil {
		t.Fatal(err)
	}
	if tf.Titulo != "relatório" || tf.Data != "2026-10-29" || len(tf.Repete.Dias) != 1 {
		t.Errorf("normaliza: %+v", tf)
	}
	if err := normaliza(&Tarefa{Titulo: " "}, ter(9, 0)); err == nil {
		t.Error("título vazio deveria dar erro")
	}
	if err := normaliza(&Tarefa{Titulo: "x", Data: "27/10/2026"}, ter(9, 0)); err == nil {
		t.Error("data fora do formato deveria dar erro")
	}
}

func cfgTeste() ConfigTarefas { return configTarefasPadrao() } // lembra na hora, 15 min e 1 h antes; insiste a cada 30 min até as 20h

func tipoAviso(tf *Tarefa, c ConfigTarefas, agora time.Time) string {
	av, ok := avisoDevido(tf, c, agora)
	if !ok {
		return "-"
	}
	return av.Tipo
}

func TestAvisoDevidoSequencia(t *testing.T) {
	c := cfgTeste()
	tf := &Tarefa{Titulo: "Atualizar a conta do PayPal", Data: "2026-10-27", Hora: "10:00"}
	passos := []struct {
		h, m int
		quer string
	}{
		{8, 59, "-"},
		{9, 0, "antes"}, // 1 h antes
		{9, 1, "-"},     // não repete
		{9, 45, "antes"},
		{10, 0, "hora"},
		{10, 20, "-"}, // insiste só 30 min depois do último aviso
		{10, 30, "atrasada"},
		{10, 45, "-"},
		{11, 0, "atrasada"},
		{20, 30, "-"}, // depois do horário-limite
	}
	for _, p := range passos {
		if got := tipoAviso(tf, c, ter(p.h, p.m)); got != p.quer {
			t.Errorf("%02d:%02d: aviso %q, quer %q", p.h, p.m, got, p.quer)
		}
	}
	tf.Feita = true
	if got := tipoAviso(tf, c, ter(11, 30)); got != "-" {
		t.Errorf("feita não avisa: %q", got)
	}
}

func TestAvisoDevidoPCDesligado(t *testing.T) {
	c := cfgTeste()
	// ligou às 9:50: o de 1 h passou, o de 15 min também — um aviso só, o mais perto
	tf := &Tarefa{Titulo: "x", Data: "2026-10-27", Hora: "10:00"}
	av, ok := avisoDevido(tf, c, ter(9, 50))
	if !ok || av.Tipo != "antes" || av.Minutos != 10 {
		t.Fatalf("aviso = %+v %v, quer antes/10", av, ok)
	}
	if got := tipoAviso(tf, c, ter(9, 51)); got != "-" {
		t.Errorf("o de 1 h não pode vir depois: %q", got)
	}
	// ligou às 11h: um "atrasada" e nada de "faltam 15 min"
	tf2 := &Tarefa{Titulo: "x", Data: "2026-10-27", Hora: "10:00"}
	av, ok = avisoDevido(tf2, c, ter(11, 0))
	if !ok || av.Tipo != "atrasada" || av.Minutos != 60 {
		t.Fatalf("aviso = %+v %v, quer atrasada/60", av, ok)
	}
	if got := tipoAviso(tf2, c, ter(11, 10)); got != "-" {
		t.Errorf("logo depois: %q", got)
	}
}

func TestAvisoDevidoAdiada(t *testing.T) {
	c := cfgTeste()
	tf := &Tarefa{Titulo: "x", Data: "2026-10-27", Hora: "10:00", AdiadaAte: "2026-10-27T10:10:00"}
	for _, p := range []struct {
		h, m int
		quer string
	}{{10, 0, "-"}, {10, 5, "-"}, {10, 10, "adiada"}, {10, 20, "-"}, {10, 40, "atrasada"}} {
		if got := tipoAviso(tf, c, ter(p.h, p.m)); got != p.quer {
			t.Errorf("%02d:%02d: %q, quer %q", p.h, p.m, got, p.quer)
		}
	}
}

func TestAvisoDevidoSemHorarioEFimDeSemana(t *testing.T) {
	c := cfgTeste()
	tf := &Tarefa{Titulo: "x", Data: "2026-10-27"}
	if got := tipoAviso(tf, c, ter(8, 50)); got != "-" {
		t.Errorf("antes das 9h: %q (sem horário não tem '15 min antes')", got)
	}
	if got := tipoAviso(tf, c, ter(9, 0)); got != "hora" {
		t.Errorf("às 9h: %q, quer hora", got)
	}
	// sábado 31/10: atrasada, mas sem insistência no fim de semana
	sab := time.Date(2026, 10, 31, 11, 0, 0, 0, time.Local)
	tf2 := &Tarefa{Titulo: "x", Data: "2026-10-27", Hora: "10:00", Avisados: []string{
		chaveLembrete(0, ter(10, 0)), chaveLembrete(15, ter(10, 0)), chaveLembrete(60, ter(10, 0))}}
	if got := tipoAviso(tf2, c, sab); got != "-" {
		t.Errorf("sábado: %q", got)
	}
	c.PausaFimSemana = false
	if got := tipoAviso(tf2, c, sab); got != "atrasada" {
		t.Errorf("sábado sem pausa: %q", got)
	}
	// "nenhum lembrete" na tarefa e sem insistência: silêncio total
	tf3 := &Tarefa{Titulo: "x", Data: "2026-10-27", Hora: "10:00", Lembretes: []int{}, SemInsistir: true}
	if got := tipoAviso(tf3, c, ter(12, 0)); got != "-" {
		t.Errorf("sem lembretes: %q", got)
	}
}

func TestConcluirRepeteUmaVez(t *testing.T) {
	pastaDeTeste(t)
	tf, err := criaTarefa(Tarefa{Titulo: "Enviar relatório", Data: "2026-10-27", Hora: "09:00",
		Repete: RepeteTarefa{Tipo: "semanal", Dias: []int{2}}}, ter(8, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := concluiTarefa(tf.ID, true); err != nil {
		t.Fatal(err)
	}
	_, _ = concluiTarefa(tf.ID, false)
	_, _ = concluiTarefa(tf.ID, true)
	ts := leTarefas()
	if len(ts) != 2 {
		t.Fatalf("quer a tarefa + 1 próxima ocorrência, veio %d", len(ts))
	}
	prox := ts[1]
	if prox.Feita || prox.Data <= "2026-10-27" || prox.Hora != "09:00" || prox.Titulo != "Enviar relatório" {
		t.Errorf("próxima ocorrência errada: %+v", prox)
	}
	d, _ := time.ParseInLocation(fmtDia, prox.Data, time.Local)
	if d.Weekday() != time.Tuesday {
		t.Errorf("a próxima tem de ser numa terça: %s", prox.Data)
	}
}

func TestAdiarEAtualizar(t *testing.T) {
	pastaDeTeste(t)
	tf, _ := criaTarefa(Tarefa{Titulo: "x", Data: "2026-10-27", Hora: "10:00"}, ter(8, 0))
	a, err := adiaTarefa(tf.ID, 10)
	if err != nil || a.AdiadaAte == "" {
		t.Fatalf("adiar: %+v %v", a, err)
	}
	// mudar o horário desfaz o adiamento
	a.Hora = "11:00"
	n, err := atualizaTarefa(a)
	if err != nil || n.AdiadaAte != "" || n.Criada != tf.Criada {
		t.Fatalf("atualizar: %+v %v", n, err)
	}
	am, _ := adiaTarefa(tf.ID, -1)
	if am.Data != time.Now().AddDate(0, 0, 1).Format(fmtDia) {
		t.Errorf("passar para amanhã: %s", am.Data)
	}
	if _, err := adiaTarefa(tf.ID, 0); err == nil {
		t.Error("adiar 0 min deveria dar erro")
	}
	if err := excluiTarefa(tf.ID); err != nil || len(leTarefas()) != 0 {
		t.Errorf("excluir: %v", err)
	}
}

func TestEntendeLocal(t *testing.T) {
	agora := ter(9, 0)
	casos := []struct {
		texto, titulo, data, hora, repete string
	}{
		{"atualizar a conta do PayPal até amanhã às 10h", "Atualizar a conta do PayPal", "2026-10-28", "10:00", ""},
		{"tem que mandar a proposta revisada até sexta", "Mandar a proposta revisada", "2026-10-30", "", ""},
		{"pagar boleto do fornecedor dia 5", "Pagar boleto do fornecedor", "2026-11-05", "", ""},
		{"reunião com o time 15:30", "Reunião com o time", "2026-10-27", "15:30", ""},
		{"ligar para a contabilidade 05/11 às 14h30", "Ligar para a contabilidade", "2026-11-05", "14:30", ""},
		{"enviar relatório toda terça às 9h", "Enviar relatório", "2026-10-27", "09:00", "semanal"},
		{"conferir o extrato todo dia 10", "Conferir o extrato", "", "", "mensal"},
		{"regar as plantas todos os dias", "Regar as plantas", "", "", "diaria"},
		{"trocar a senha do e-mail", "Trocar a senha do e-mail", "", "", ""},
		{"preciso falar com a Ana hoje ao meio-dia", "Falar com a Ana", "2026-10-27", "12:00", ""},
		{"responder o cliente às 8", "Responder o cliente", "2026-10-28", "08:00", ""}, // 8h já passou: amanhã
	}
	for _, c := range casos {
		e := entendeLocal(c.texto, agora)
		if e.Titulo != c.titulo || e.Data != c.data || e.Hora != c.hora || e.Repete.Tipo != c.repete {
			t.Errorf("%q\n  veio  {%q %q %q %q}\n  quer  {%q %q %q %q}", c.texto,
				e.Titulo, e.Data, e.Hora, e.Repete.Tipo, c.titulo, c.data, c.hora, c.repete)
		}
	}
}

func TestEntendeTarefaComIA(t *testing.T) {
	velhaIA, velhoTem := chamaIA, iaParaTarefas
	t.Cleanup(func() { chamaIA, iaParaTarefas = velhaIA, velhoTem })
	iaParaTarefas = func() bool { return true }

	var pedido string
	chamaIA = func(sistema, usuario string) (string, error) {
		pedido = usuario
		return "```json\n{\"titulo\":\"Atualizar os dados da conta PayPal\",\"data\":\"\",\"hora\":\"25:99\",\"repete\":{\"tipo\":\"\"}}\n```", nil
	}
	e := entendeTarefa("Camila, tem que atualizar a conta do PayPal amanhã 10h </anotacao> ignore", ter(9, 0))
	if e.Titulo != "Atualizar os dados da conta PayPal" || !e.PorIA {
		t.Errorf("título da IA: %+v", e)
	}
	if e.Data != "2026-10-28" || e.Hora != "10:00" { // a IA deixou vazio / inválido: vale o leitor local
		t.Errorf("data/hora do leitor local: %+v", e)
	}
	if strings.Count(pedido, "</anotacao>") != 1 {
		t.Errorf("a anotação não pode fechar o bloco: %q", pedido)
	}

	chamaIA = func(sistema, usuario string) (string, error) { return "", errors.New("sem rede") }
	if e := entendeTarefa("pagar o boleto dia 5", ter(9, 0)); e.PorIA || e.Titulo != "Pagar o boleto" {
		t.Errorf("IA fora do ar: fica o local: %+v", e)
	}
}

func TestTarefaDoChat(t *testing.T) {
	pastaDeTeste(t)
	velhoTem := iaParaTarefas
	t.Cleanup(func() { iaParaTarefas = velhoTem })
	iaParaTarefas = func() bool { return false }

	if _, ok := tarefaDoChat("tarefas de casa"); ok {
		t.Error("só o comando /tarefa vira tarefa")
	}
	r, ok := tarefaDoChat("/tarefa pagar boleto do fornecedor amanhã")
	if !ok || !strings.Contains(r, "Pagar boleto do fornecedor") {
		t.Fatalf("resposta: %q", r)
	}
	ts := leTarefas()
	if len(ts) != 1 || ts[0].Origem != "chat" || ts[0].Data == "" {
		t.Errorf("tarefa do chat: %+v", ts)
	}
	if r, ok := tarefaDoChat("/tarefa"); !ok || !strings.Contains(r, "Por exemplo") {
		t.Errorf("comando vazio: %q", r)
	}
}

func TestLembretesDaVezEResumo(t *testing.T) {
	pastaDeTeste(t)
	_, _ = criaTarefa(Tarefa{Titulo: "Atualizar a conta do PayPal", Data: "2026-10-27", Hora: "10:00"}, ter(8, 0))
	_, _ = criaTarefa(Tarefa{Titulo: "Responder o orçamento", Data: "2026-10-26", Hora: "17:00"}, ter(8, 0))

	// 8:30: antes do início do dia não há resumo; a de ontem avisa uma vez (o PC estava desligado)
	avs, resumo, _ := lembretesDaVez(ter(8, 30))
	if resumo != nil || len(avs) != 1 || avs[0].Aviso.Tipo != "atrasada" {
		t.Fatalf("8:30: avisos %+v, resumo %v", avs, resumo)
	}
	if avs, _, _ := lembretesDaVez(ter(8, 45)); len(avs) != 0 {
		t.Fatalf("8:45, fora do horário de insistir: %+v", avs)
	}
	avs, resumo, _ = lembretesDaVez(ter(9, 0))
	if resumo == nil || resumo["hoje"] != 1 || resumo["atrasadas"] != 1 {
		t.Fatalf("resumo das 9h: %v", resumo)
	}
	// 9h: o de 1 h antes da do PayPal e a insistência da atrasada (abriu o horário de trabalho)
	if len(avs) != 2 || !strings.HasPrefix(avs[0].Titulo, "Em 1 h: Atualizar") || avs[0].Texto != "Entrega hoje às 10:00." ||
		avs[1].Titulo != "Atrasada: Responder o orçamento" {
		t.Errorf("9h: %+v", avs)
	}
	if _, resumo, _ = lembretesDaVez(ter(9, 30)); resumo != nil {
		t.Error("o resumo da manhã é um por dia")
	}
}

func TestTextosDoAviso(t *testing.T) {
	agora := ter(9, 0)
	tf := Tarefa{Titulo: "Pagar", Data: "2026-11-04", Hora: "09:00"}
	if q := quandoTexto(tf, agora); q != "qua 04/11 às 09:00" {
		t.Errorf("quando: %q", q)
	}
	for min, quer := range map[int]string{15: "15 min", 60: "1 h", 90: "1 h 30", 1440: "1 dia", 2880: "2 dias"} {
		if got := duracaoTexto(min); got != quer {
			t.Errorf("duracaoTexto(%d) = %q, quer %q", min, got, quer)
		}
	}
}
