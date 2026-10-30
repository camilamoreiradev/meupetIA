#!/usr/bin/env python
# teste-ponte-aprovacao.py - o Hermes pede aprovação no meio da pergunta e a ponte responde.
#
# Incidente de 30/10/2026: a dona pediu ao boneco para mexer no DNS da GoDaddy e todo comando
# de terminal voltava "BLOCKED: Command approval was withdrawn before the user answered (the
# attached client cannot answer approval requests (update the Hermes app))". O Hermes só manda
# a pergunta a quem avisou `client.capabilities {server_requests: true}` — e a ponte não avisava.
#
# O que o teste exige (Hermes de mentira, nada sobe de verdade):
#   1) ao conectar, a ponte avisa que responde pedidos servidor→cliente
#   2) a aprovação vira uma linha para o app (o cartão) e a escolha volta ao Hermes com o id dele
#   3) um pedido que a ponte não sabe mostrar (clarify) é recusado NA HORA com -32601
#   4) `request.cancel` tira o cartão da tela; resposta a pedido retirado não vai ao Hermes
import asyncio
import contextlib
import io
import json
import os
import sys

AQUI = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, AQUI)
import chat_bridge as cb  # noqa: E402


class HermesDeMentira:
    """O /api/ws: responde as RPCs da conexão e entrega os quadros que o teste enfileira."""

    def __init__(self) -> None:
        self.enviados: list[dict] = []
        self.entrada: asyncio.Queue = asyncio.Queue()

    async def send(self, bruto: str) -> None:
        d = json.loads(bruto)
        self.enviados.append(d)
        metodo = d.get("method")
        if metodo == "client.capabilities":
            await self.entrada.put({"jsonrpc": "2.0", "id": d["id"], "result": {"server_requests": ["approval"]}})
        elif metodo == "session.create":
            await self.entrada.put({"jsonrpc": "2.0", "id": d["id"],
                                    "result": {"session_id": "s1", "stored_session_id": "c1", "info": {}}})

    async def recv(self) -> str:
        return json.dumps(await self.entrada.get())

    async def close(self) -> None:
        pass


def evento(tipo: str, payload: dict) -> dict:
    return {"jsonrpc": "2.0", "method": "event", "params": {"type": tipo, "payload": payload}}


def falha(msg: str) -> int:
    print(f"[teste] FALHA: {msg}")
    return 1


async def principal() -> int:
    hermes = HermesDeMentira()

    async def conecta(*_a, **_k):
        return hermes

    cb.websockets.connect = conecta
    os.environ.pop("PETAGENT_CONVERSA", None)
    bk = cb.Backend(1)

    # ---- 1) a ponte avisa ao conectar ----
    if not await bk.conectar():
        return falha("conectar() não completou com o Hermes de mentira")
    cap = [d for d in hermes.enviados if d.get("method") == "client.capabilities"]
    if not cap or cap[0].get("params") != {"server_requests": True}:
        return falha(f"a ponte não mandou client.capabilities {{server_requests: true}}: {hermes.enviados}")
    print("[teste] 1) client.capabilities enviado na conexão")

    # ---- 2) aprovação no meio da pergunta ----
    await hermes.entrada.put({"jsonrpc": "2.0", "id": "srq-clar", "method": "clarify",
                              "params": {"session_id": "s1", "questions": []}})
    await hermes.entrada.put({"jsonrpc": "2.0", "id": "srq-1", "method": "approval",
                              "params": {"session_id": "s1", "request_id": "r1", "command": "pwsh gd_list.ps1",
                                         "description": "consulta a API da GoDaddy", "tool_name": "terminal",
                                         "choices": ["once", "session", "always", "deny"]}})

    async def dona_clica():
        # espera o cartão chegar e responde como o app (pelo stdin -> responde_aprovacao)
        while "srq-1" not in bk.aprovacoes:
            await asyncio.sleep(0.01)
        await bk.responde_aprovacao("srq-1", "once")
        await hermes.entrada.put(evento("message.delta", {"text": "seis registros"}))
        await hermes.entrada.put(evento("message.complete", {}))

    saida = io.StringIO()
    with contextlib.redirect_stdout(saida):
        clique = asyncio.create_task(dona_clica())
        resp, _ = await asyncio.wait_for(bk._perguntar_uma("liste os registros", timeout=10), 10)
        await clique

    if resp != "seis registros":
        return falha(f"a pergunta deveria terminar com a resposta depois da aprovação, veio {resp!r}")
    linhas = [json.loads(l) for l in saida.getvalue().splitlines() if l.strip()]
    cartoes = [l for l in linhas if l.get("progresso") == "aprovacao"]
    if len(cartoes) != 1:
        return falha(f"esperava UMA linha de aprovação para o app, vieram {linhas}")
    ap = cartoes[0]["aprovacao"]
    if ap.get("pedido") != "srq-1" or ap.get("comando") != "pwsh gd_list.ps1" or ap.get("opcoes") != ["once", "session", "always", "deny"]:
        return falha(f"o cartão chegou incompleto: {ap}")
    resposta = [d for d in hermes.enviados if d.get("id") == "srq-1"]
    if resposta != [{"jsonrpc": "2.0", "id": "srq-1", "result": {"choice": "once"}}]:
        return falha(f"a escolha não voltou ao Hermes no formato do contrato: {resposta}")
    print("[teste] 2) aprovação repassada ao app e escolha devolvida ao Hermes")

    # ---- 3) clarify recusado na hora ----
    recusa = [d for d in hermes.enviados if d.get("id") == "srq-clar"]
    if len(recusa) != 1 or recusa[0].get("error", {}).get("code") != -32601:
        return falha(f"o clarify deveria ser recusado com -32601: {recusa}")
    print("[teste] 3) pedido sem tratamento recusado com -32601")

    # ---- 4) o Hermes retira a pergunta ----
    await hermes.entrada.put({"jsonrpc": "2.0", "id": "srq-2", "method": "approval",
                              "params": {"session_id": "s1", "request_id": "r2", "command": "rm x"}})
    await hermes.entrada.put(evento("request.cancel", {"id": "srq-2", "method": "approval", "reason": "timeout"}))
    await hermes.entrada.put(evento("message.complete", {}))
    saida = io.StringIO()
    with contextlib.redirect_stdout(saida):
        await asyncio.wait_for(bk._perguntar_uma("outra", timeout=10), 10)
    linhas = [json.loads(l) for l in saida.getvalue().splitlines() if l.strip()]
    if not any(l.get("progresso") == "aprovacao-fim" and l.get("texto") == "srq-2" for l in linhas):
        return falha(f"request.cancel deveria tirar o cartão da tela: {linhas}")
    antes = len(hermes.enviados)
    await bk.responde_aprovacao("srq-2", "once")
    if len(hermes.enviados) != antes:
        return falha("resposta a uma aprovação já retirada não pode ir ao Hermes")
    print("[teste] 4) request.cancel tira o cartão; resposta atrasada descartada")

    print("[teste] PASSOU")
    return 0


if __name__ == "__main__":
    raise SystemExit(asyncio.run(principal()))
