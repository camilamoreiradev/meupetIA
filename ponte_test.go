package main

import (
	"bufio"
	"encoding/json"
	"io"
	"testing"
	"time"
)

// TestPonteNaoTrocaRespostas — a resposta ATRASADA de uma pergunta não pode virar a
// resposta da pergunta seguinte.
//
// Cenário real: a pergunta 1 estoura o prazo do app (que desiste e avisa), mas a
// ponte continua trabalhando nela e manda progresso e, depois, a resposta. Quando o
// dono faz a pergunta 2, a primeira linha "de resposta" que chega é a da pergunta 1.
//
// Aqui a ponte é de mentira (dois io.Pipe) e o prazo é encurtado para 300 ms.
func TestPonteNaoTrocaRespostas(t *testing.T) {
	prazoAntes := prazoPonte
	prazoPonte = 300 * time.Millisecond
	defer func() { prazoPonte = prazoAntes }()

	pedidosR, pedidosW := io.Pipe() // app -> stdin da ponte
	saidaR, saidaW := io.Pipe()     // stdout da ponte -> app
	defer pedidosW.Close()
	defer saidaW.Close()
	ligaPonte(pedidosW, saidaR)

	pedidos := bufio.NewReader(pedidosR)
	escreve := func(v map[string]any) {
		b, _ := json.Marshal(v)
		_, _ = saidaW.Write(append(b, '\n'))
	}
	leID := func() any { // a ponte recebe um pedido e devolve o id dele (se houver)
		l, err := pedidos.ReadString('\n')
		if err != nil {
			t.Fatalf("a ponte não recebeu o pedido: %v", err)
		}
		var p map[string]any
		_ = json.Unmarshal([]byte(l), &p)
		return p["id"]
	}

	escreve(map[string]any{"ok": true, "pronto": true})
	for fim := time.Now().Add(2 * time.Second); ; {
		if pronta, _ := estadoPonte(); pronta {
			break
		}
		if time.Now().After(fim) {
			t.Fatal("handshake não foi aceito")
		}
		time.Sleep(10 * time.Millisecond)
	}

	a := NewApp()

	// pergunta 1: a ponte demora mais que o prazo -> o app desiste, mas AVISA em vez de
	// cair na reserva: a ponte segue trabalhando nela e a reserva repetiria a pergunta,
	// gastando créditos em dobro
	type resultado struct {
		resp string
		ok   bool
	}
	feito1 := make(chan resultado)
	go func() { r, ok := a.perguntaPonte("pergunta 1"); feito1 <- resultado{r, ok} }()
	id1 := leID()
	if r := <-feito1; !r.ok || r.resp == "" {
		t.Fatalf("a pergunta 1 deveria estourar o prazo com aviso e sem reserva (ok=true), veio %+v", r)
	}
	// ...mas a ponte segue trabalhando nela e manda progresso
	escreve(map[string]any{"ok": true, "progresso": "passo", "texto": "terminal", "id": id1})

	// pergunta 2: a resposta atrasada da 1 chega antes da resposta da 2
	feito2 := make(chan string)
	go func() { r, _ := a.perguntaPonte("pergunta 2"); feito2 <- r }()
	id2 := leID()
	go func() {
		escreve(map[string]any{"ok": true, "id": id1, "resposta": "resposta da pergunta 1"})
		escreve(map[string]any{"ok": true, "id": id2, "resposta": "resposta da pergunta 2"})
	}()

	select {
	case r := <-feito2:
		if r != "resposta da pergunta 2" {
			t.Fatalf("pergunta 2 recebeu %q — resposta trocada", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pergunta 2 não terminou")
	}
}

// TestPonteAprovacaoNoMeioDaPergunta — o Hermes pede aprovação NO MEIO de uma pergunta, e a
// pergunta só termina depois da resposta. Se RespondeAprovacao esperasse ponteMu (que a
// pergunta segura), as duas ficariam se esperando para sempre.
func TestPonteAprovacaoNoMeioDaPergunta(t *testing.T) {
	pedidosR, pedidosW := io.Pipe()
	saidaR, saidaW := io.Pipe()
	defer pedidosW.Close()
	defer saidaW.Close()
	ligaPonte(pedidosW, saidaR)

	pedidos := bufio.NewReader(pedidosR)
	escreve := func(v map[string]any) {
		b, _ := json.Marshal(v)
		_, _ = saidaW.Write(append(b, '\n'))
	}
	le := func() map[string]any {
		l, err := pedidos.ReadString('\n')
		if err != nil {
			t.Fatalf("a ponte não recebeu a linha: %v", err)
		}
		var p map[string]any
		_ = json.Unmarshal([]byte(l), &p)
		return p
	}

	escreve(map[string]any{"ok": true, "pronto": true})
	for fim := time.Now().Add(2 * time.Second); ; {
		if pronta, _ := estadoPonte(); pronta {
			break
		}
		if time.Now().After(fim) {
			t.Fatal("handshake não foi aceito")
		}
		time.Sleep(10 * time.Millisecond)
	}

	a := NewApp()
	feito := make(chan string)
	go func() { r, _ := a.perguntaPonte("liste os registros"); feito <- r }()
	id := le()["id"]

	escreve(map[string]any{"ok": true, "id": id, "progresso": "aprovacao",
		"aprovacao": map[string]any{"pedido": "srq-abc", "comando": "gd_list.ps1", "opcoes": []string{"once", "deny"}}})

	if msg := a.RespondeAprovacao("srq-abc", "talvez"); msg == "" {
		t.Fatal("escolha fora da lista deveria ser recusada")
	}
	// io.Pipe não tem buffer (o cano de verdade tem): a ponte de mentira lê ao mesmo tempo
	recebida := make(chan map[string]any, 1)
	go func() { recebida <- le() }()
	respondeu := make(chan string)
	go func() { respondeu <- a.RespondeAprovacao("srq-abc", "once") }()
	select {
	case msg := <-respondeu:
		if msg != "" {
			t.Fatalf("a resposta da aprovação falhou: %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RespondeAprovacao travou esperando a pergunta em andamento")
	}
	if p := <-recebida; p["aprovacao"] != "srq-abc" || p["escolha"] != "once" {
		t.Fatalf("a ponte recebeu %v, esperava a aprovação srq-abc/once", p)
	}

	escreve(map[string]any{"ok": true, "id": id, "resposta": "seis registros"})
	select {
	case r := <-feito:
		if r != "seis registros" {
			t.Fatalf("a pergunta recebeu %q", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a pergunta não terminou depois da aprovação")
	}
}
