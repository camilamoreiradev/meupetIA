#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""Ponte do chat do PetAgent — BACKEND PRÓPRIO do boneco (`hermes serve` + /api/ws).

Arquitetura validada em 02/10/2026 (com números):
    * `hermes -z`        -> sobe agente novo a cada mensagem ......... ~30-290 s ✗
    * `hermes acp`       -> rápido para conversa, mas as FERRAMENTAS
                            travam ("Creating new local environment") ✗
    * `hermes serve`     -> processo PRÓPRIO, agente quente, e as ferramentas
                            FUNCIONAM (testado: executou terminal em 23,6 s) ✓

    app (Go) --stdin/stdout--> esta ponte --WebSocket--> hermes serve (próprio)

Por que isto é seguro para o usuário: o backend é um processo separado, do MESMO
perfil (mesma memória/personalidade/skills), sem reiniciar nem editar nada do
gateway que já está rodando.

Protocolo com o app (1 JSON por linha):
    app -> {"id": 1, "prompt": "texto", "conversa": "<id guardado>"?, "anexos": [...]?}
    app -> {"id": 2, "op": "listar" | "abrir" | "nova" | "renomear" | "excluir", ...}
    app <- {"ok": true, "pronto": true, ...}          (handshake)
    app <- {"ok": true, "resposta": "...", "segundos": 3.2, "conversa": "<id guardado>"}
    app <- {"ok": true, "id": 2, "dados": {...}}      (resposta de uma "op")
    app <- {"ok": true, "id": 1, "progresso": "aprovacao", "aprovacao": {...}}  (ver abaixo)
    app -> {"aprovacao": "<pedido>", "escolha": "once" | "session" | "always" | "deny"}

APROVAÇÕES (30/10/2026). Comando perigoso (terminal, computer_use…) o Hermes só roda com o
"sim" de alguém. Ele pergunta ao CLIENTE conectado por um pedido servidor→cliente
(`{"id": "srq-…", "method": "approval", ...}`) — mas só a quem avisou, ao conectar, que sabe
responder (`client.capabilities {server_requests: true}`). A ponte não avisava: o Hermes
retirava a pergunta na hora ("the attached client cannot answer approval requests (update the
Hermes app)") e a dona não conseguia autorizar nada pelo chat. Agora a ponte avisa, repassa a
pergunta ao app (o cartão no chat) e devolve a escolha. A resposta do app chega pelo stdin NO
MEIO da pergunta — por isso o stdin tem um leitor próprio (_le_stdin), separado do laço serial.

CONVERSAS (22/10/2026, pedido da dona: "criar chats como no Claude, e a anterior fica
salva"). Cada conversa é uma SESSÃO do Hermes, no state.db do home espelhado (o do boneco,
não o do dono). O Hermes tem dois ids: o GUARDADO (`stored_session_id`, a chave da linha no
banco — é o que a lista mostra e o que o app guarda) e o de EXECUÇÃO (`session_id`, vale só
enquanto o `hermes serve` vive). O app só conhece o guardado; aqui ele vira o de execução por
`session.resume`. O título, o Hermes dá sozinho depois da primeira resposta.

ANEXOS: imagem vai por `image.attach_bytes` (o modelo VÊ a imagem); texto e PDF (o app já
extraiu o texto do PDF) vão por `file.attach`, que devolve uma referência `@file:` para pôr
na mensagem. Os dois entram na fila da sessão e seguem com o próximo `prompt.submit`.
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import re
import secrets
import socket
import subprocess
import sys
import time

try:
    import websockets
except ImportError:  # pragma: no cover
    print(json.dumps({"ok": False, "erro": "o pacote websockets não está instalado no ambiente do Hermes"}))
    raise SystemExit(2)

AQUI = os.path.dirname(os.path.abspath(__file__))
LOCALAPPDATA = os.environ.get("LOCALAPPDATA", "")


def _achar_hermes() -> list[str]:
    """Comando do backend próprio: um `hermes` que FUNCIONE — sem alterar a
    instalação do dono (regra do plugin: o PetAgent não pode exigir que se mexa no
    Hermes instalado).

    Duas armadilhas pagas em 03/10/2026, quando o chat do boneco caiu calado com
    "backend não subiu":

    1. O Hermes mantém mais de um venv, e o `venv-dashboard` é o que ELE MESMO
       montou com os pins certos do stack web (`fastapi==0.133.1` +
       `starlette==1.3.1`, iguais aos do `pyproject.toml` dele). O venv principal
       ficou com fastapi estale (0.115.0) e o `hermes serve` morria no import com
       `Router.__init__() got an unexpected keyword argument 'on_startup'`.
    2. Não adianta chamar `Scripts\\hermes.exe`: esse launcher amarra um
       interpretador que IGNORA o ambiente limpo e segue pegando o venv errado
       (medido: hermes.exe morria, `python.exe -m hermes_cli.main` subiu em ~6 s).
       Chamando o python do venv escolhido, o venv escolhido manda.

    E o PYTHONPATH herdado precisa sair do caminho — ver `_env_do_backend()`.
    """
    venvs = [
        os.environ.get("PETAGENT_HERMES_VENV", ""),
        os.path.join(LOCALAPPDATA, "hermes", "venv-dashboard"),
        os.path.join(LOCALAPPDATA, "hermes", "hermes-agent", "venv"),
    ]
    for v in venvs:
        if v and os.path.exists(os.path.join(v, "Scripts", "python.exe")):
            return [os.path.join(v, "Scripts", "python.exe"), "-m", "hermes_cli.main"]
    # Hermes 0.21+ (Windows): não há mais venv fixo. O lançador `bin\hermes.exe` escolhe
    # o python e o venv da instalação, tira PYTHONPATH/PYTHONHOME e respeita HERMES_HOME
    # (o home espelhado) — a armadilha 2 acima era do lançador antigo.
    novo = os.path.join(LOCALAPPDATA, "hermes", "bin", "hermes.exe")
    if os.path.exists(novo):
        return [novo]
    # nada encontrado: o log de "não encontrado" aponta onde eu olhei
    return [os.path.join(venvs[-1], "Scripts", "python.exe"), "-m", "hermes_cli.main"]


HERMES_CMD = _achar_hermes()


# ── home espelhado: herda o home do Hermes, mas sem o MCP que polui a tela ──────
#
# MEDIDO em 03/10/2026 (vigia por evento de criação de processo): o backend do chat
# sobe TODOS os MCPs do dono — inclusive `cua-driver` — e CADA instância dele abre uma
# janela `Cua.AgentCursorOverlay` de 3840x1440, isto é, as DUAS telas. Invisível, mas
# presente: atrapalha qualquer ferramenta que capture ou grave UMA janela no PC do dono
# (a "janela do topo" vira ela). O boneco não precisa de computer-use para conversar;
# o dono precisa da tela limpa.
#
# Como, sem tocar no Hermes do dono: `HERMES_HOME` é o que define qual `config.yaml` é
# lido — o próprio código do Hermes diz isso ("profile switches change HERMES_HOME and
# therefore get_config_path()"). Então o backend sobe com um HOME ESPELHADO: tudo do
# home real entra por junction (pasta) ou hardlink (arquivo) — skills, plugins,
# memórias, sessões, .env — e o `config.yaml` é um arquivo NOSSO: cópia do dele sem a
# entrada `mcp_servers.cua-driver`, regerada a cada subida (não envelhece). Os bancos
# SQLite e as travas da raiz ficam DE FORA (ver _fora_do_espelho).
HOME_ESPELHO = os.path.join(os.environ.get("LOCALAPPDATA", AQUI), "petagent", "hermes-home")
MCP_A_REMOVER = ("cua-driver",)
BLOCO_CLARIFY = '\n\n# PetAgent: o boneco NAO tem tela de escolhas e o `clarify` espera 3600 s por padrao\n# (medido 10/10/2026: o agente travava em clarify.request e a ponte estourava aos 300 s).\n# Com 8 s ele pergunta, ninguem responde e ele SEGUE SOZINHO, respondendo em texto normal.\n# NAO usar 0: no Hermes 0 = espera ILIMITADA.\nclarify:\n  timeout: 8\n'
# TETO DE ITERAÇÕES DA IA POR MENSAGEM (pedido da dona, 13/10/2026): o laço que gasta
# crédito é o do agente — chama o modelo, roda ferramenta, chama de novo. O config do dono
# deixa 150 voltas por mensagem (e 250 por subagente): um agente em loop queimava os
# créditos de uma vez. Ao chegar no teto o Hermes pede um resumo ao modelo e responde em
# texto normal. ⚠️ NÃO usar 0, -1 ou "none": no Hermes isso é ILIMITADO. O app Go repete
# este número na reserva lenta (`maxIteracoesIA` em chat.go) — os dois andam juntos.
MAX_ITERACOES_IA = 20


# IA SÓ DO BONECO (22/10/2026, painel Inteligência do app): quando a dona escolhe um modelo e
# uma chave só para o boneco, o app manda pela VARIÁVEL DE AMBIENTE desta ponte:
#   PETAGENT_IA        = {"provedor", "modelo", "base_url", "var"}  (JSON)
#   PETAGENT_IA_CHAVE  = a chave
# e o espelho passa a dizer esse modelo (config.yaml) e a ter essa chave (.env PRÓPRIO, no
# lugar do hardlink). Variável de ambiente sozinha não serve: o Hermes carrega o .env com
# override=True e a chave do dono venceria. O Hermes do dono NÃO é tocado.
# Variáveis de chave de IA que saem do .env do espelho quando o boneco tem IA própria (para
# não sobrar a chave do dono de outro provedor):
VARS_CHAVE_IA = ("OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_TOKEN",
                 "GOOGLE_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY", "XAI_API_KEY")


def _ia_do_boneco() -> dict:
    """A IA só do boneco (vazio = usa a do Hermes, como sempre)."""
    try:
        d = json.loads(os.environ.get("PETAGENT_IA") or "{}")
    except json.JSONDecodeError:
        return {}
    chave = os.environ.get("PETAGENT_IA_CHAVE") or ""
    if not isinstance(d, dict) or not chave or not all(d.get(k) for k in ("provedor", "modelo", "var")):
        return {}
    d["chave"] = chave
    return d


def _troca_modelo(texto: str, ia: dict) -> str:
    """No bloco `model:`, põe default/provider/base_url do boneco e tira o que prende ao
    provedor antigo (api_mode, api_key/api fixos). O app faz o MESMO no Hermes do dono quando
    a escolha é "igual ao Hermes" (inteligencia.go, trocaModeloNoConfig) — os dois andam juntos."""
    novos = [("default", ia["modelo"]), ("provider", ia["provedor"]), ("base_url", ia.get("base_url") or "")]
    # mesmo provedor do dono = só o modelo troca (api_mode etc. do provedor ficam)
    prov_antes = ""
    no_bloco = False
    for l in texto.splitlines():
        if l and l[0] not in " \t" and not l.startswith("#"):
            no_bloco = l.partition(":")[0].strip() == "model"
        elif no_bloco and l.strip().startswith("provider:") and len(l) - len(l.lstrip()) <= 2:
            prov_antes = l.split(":", 1)[1].strip().strip("'\"")
    trocou_provedor = prov_antes != ia["provedor"]
    saida, vistos = [], set()
    dentro = achou = False

    def falta():
        for k, v in novos:
            if k not in vistos and v:
                saida.append(f'  {k}: "{v}"')

    for l in texto.splitlines():
        if l and l[0] not in " \t" and not l.startswith("#"):
            if dentro:
                falta()
            nome, _, resto = l.partition(":")
            dentro = nome.strip() == "model"
            if dentro:
                achou = True
                if resto.strip():          # forma curta `model: "x"` vira bloco
                    saida.append("model:")
                    continue
            saida.append(l)
            continue
        if dentro:
            s = l.strip()
            recuo = l[:len(l) - len(l.lstrip())]
            k = s.split(":", 1)[0].strip() if ":" in s and not s.startswith("#") else ""
            if k and len(recuo) <= 2 and k in ("default", "model", "provider", "base_url",
                                               "api_mode", "api_key", "api"):
                k = "default" if k == "model" else k
                if k in ("api_mode", "api_key", "api"):
                    if not trocou_provedor:
                        saida.append(l)
                    continue
                if k in vistos:
                    continue
                vistos.add(k)
                v = dict(novos)[k]
                if v:
                    saida.append(f'{recuo}{k}: "{v}"')
                continue
        saida.append(l)
    if dentro:
        falta()
    if not achou:
        saida += ["", "model:"]
        falta()
    return "\n".join(saida) + "\n"


def _env_do_espelho(real: str, ia: dict) -> str:
    """O .env do espelho com a chave do boneco: o do dono SEM as chaves de IA + a do boneco."""
    linhas = []
    try:
        with open(os.path.join(real, ".env"), "r", encoding="utf-8") as fh:
            for l in fh.read().splitlines():
                nome = l.strip().removeprefix("export ").split("=", 1)[0].strip()
                if nome not in VARS_CHAVE_IA:
                    linhas.append(l)
    except OSError:
        pass
    while linhas and not linhas[-1].strip():
        linhas.pop()
    linhas += ["", "# PetAgent: IA só do boneco (painel Inteligência). Refeito a cada subida.",
               f'{ia["var"]}={ia["chave"]}']
    return "\n".join(linhas) + "\n"


def _home_real() -> str:
    return os.environ.get("HERMES_HOME") or os.path.join(
        os.environ.get("LOCALAPPDATA", AQUI), "hermes")


def _tira_mcp(texto: str, nomes) -> str:
    """Remove, de um config.yaml em texto, o bloco de cada MCP em `nomes`.

    Textual de propósito: não depende de PyYAML no venv que roda a ponte, e o
    config.yaml do dono é legível. O bloco vai da linha `  nome:` até a primeira
    linha com indentação menor ou igual (ou o fim).
    """
    linhas = texto.splitlines(True)
    saida, i = [], 0
    while i < len(linhas):
        linha = linhas[i]
        chave = linha.strip()
        alvo = any(chave in (n + ":", '"' + n + '":') for n in nomes) and linha[:1] in (" ", "\t")
        if alvo:
            indent = len(linha) - len(linha.lstrip())
            i += 1
            while i < len(linhas):
                prox = linhas[i]
                if not prox.strip():
                    i += 1
                    continue
                if (len(prox) - len(prox.lstrip())) > indent:
                    i += 1
                    continue
                break
            continue
        saida.append(linha)
        i += 1
    return "".join(saida)


def _fora_do_espelho(nome: str) -> bool:
    """Arquivos da raiz que NÃO entram no espelho por hardlink.

    Bancos SQLite (com o -wal/-shm/-journal deles) e travas/pids de processo. O SQLite
    acha o -wal/-shm pelo CAMINHO do banco: com o mesmo arquivo aberto por dois caminhos
    (hermes\\state.db no gateway do dono, hermes-home\\state.db no backend do boneco), cada
    processo pode acabar com o próprio WAL para o MESMO banco — receita de corrupção do
    histórico do dono. Fora do espelho, o boneco cria os bancos dele; memórias, skills,
    plugins e personalidade continuam os do dono. (Pastas vão por junction: lá dentro o
    caminho real é um só e o -wal cai no mesmo lugar.)
    """
    n = nome.lower()
    return n in ("config.yaml", "config.yml") or n.endswith(
        (".db", ".sqlite", ".sqlite3", "-wal", "-shm", "-journal", ".lock", ".pid"))


def _home_do_backend() -> str:
    """Prepara o home espelhado e devolve o caminho (vai em HERMES_HOME)."""
    real = _home_real()
    try:
        os.makedirs(HOME_ESPELHO, exist_ok=True)
        # 1) espelha o resto (junction para pasta, hardlink para arquivo)
        for nome in os.listdir(real):
            origem = os.path.join(real, nome)
            destino = os.path.join(HOME_ESPELHO, nome)
            if _fora_do_espelho(nome):
                # espelho de versão anterior: o banco/trava estava LIGADO ao do dono. Desfaz
                # o link (some só o nome no espelho; o arquivo do dono fica intacto).
                if (nome.lower() not in ("config.yaml", "config.yml") and os.path.isfile(destino)
                        and os.path.samefile(destino, origem)):
                    try:
                        os.remove(destino)
                        log(f"espelho: {nome} desligado do home do dono")
                    except OSError as e:
                        log(f"AVISO: {nome} segue ligado ao do dono ({e})")
                continue
            if os.path.isdir(origem):
                if not os.path.exists(destino):
                    subprocess.run(["cmd", "/c", "mklink", "/J", destino, origem],
                                   capture_output=True)
                continue
            # arquivo: link REFEITO a cada subida. O Hermes regrava alguns TROCANDO o arquivo
            # (o .env ao mudar a chave, por exemplo) e o hardlink antigo seguia apontando para
            # a versão velha.
            try:
                if os.path.lexists(destino):
                    os.remove(destino)
            except OSError as e:
                log(f"AVISO: não consegui renovar o link de {nome} ({e})")
                continue
            subprocess.run(["cmd", "/c", "mklink", "/H", destino, origem],
                           capture_output=True)
        # 2) config.yaml: cópia do dele sem os MCPs que poluem a tela
        with open(os.path.join(real, "config.yaml"), "r", encoding="utf-8") as fh:
            bruto = fh.read()
        limpo = _tira_mcp(bruto, MCP_A_REMOVER)
        # MENOS ESFORÇO DE RACIOCÍNIO NO BONECO (10/10/2026).
        #
        # Medido: uma pergunta simples levou 224 s e o log mostrou 3.802 eventos de
        # "pensamento" (reasoning.delta) + 5 chamadas de ferramenta (3× terminal, 2×
        # session_search). O `agent.reasoning_effort` estava em `medium` — herdado do dono.
        # O boneco é CHAT: `low` responde muito mais rápido. Valores aceitos: none, minimal,
        # low, medium, high. Isto muda SÓ o home do boneco; o config do dono não é tocado.
        limpo, trocou = _troca_reasoning(limpo)
        log("config do backend: reasoning_effort=%s (era medium)"
            % ("low" if trocou else "ja estava fora do padrao"))
        # TETO DE ITERAÇÕES (ver MAX_ITERACOES_IA). No `hermes serve` quem manda é a variável
        # HERMES_TUI_MAX_TURNS (_env_do_backend); aqui o config espelhado passa a dizer o
        # mesmo e os SUBAGENTES (delegation), que não têm variável, também ganham o teto.
        limpo, mudou = _limita_iteracoes(limpo)
        log("config do backend: max_turns e delegation.max_iterations <= %d (%s)"
            % (MAX_ITERACOES_IA, "ajustado" if mudou else "ja estava dentro"))
        # O BONECO NÃO TEM TELA DE ESCOLHAS — e o `clarify` espera 1 HORA por padrão.
        #
        # Medido em 10/10/2026 (dono: "pergunta simples levou 205 s" e depois 300 s): o agente
        # chamou a ferramenta `clarify` (que mostra OPÇÕES para o usuário clicar), o chat do
        # boneco não tem como exibir as opções e a ponte não responde à requisição — e
        # `tools/clarify_gateway.py` espera `clarify.timeout`, cujo padrão é 3600 s (1 hora).
        # O agente ficava travado esperando; a ponte estourava aos 300 s e o app caía na reserva
        # lenta, que repetia a mesma pergunta. Log real: "tool.generating name=clarify" →
        # "clarify.request" → 300 s de silêncio.
        #
        # Com 8 s o agente pergunta, ninguém responde e ele SEGUE SOZINHO, respondendo em texto
        # normal — que o balão do boneco mostra. ⚠️ NÃO usar 0: no Hermes 0 significa espera
        # ILIMITADA (o oposto do que parece) e travaria para sempre.
        if not any(l.strip().startswith("clarify:") for l in limpo.splitlines()):
            limpo = limpo.rstrip() + BLOCO_CLARIFY
            log("config do backend: clarify.timeout=8 (o boneco nao tem tela de escolhas)")
        else:
            log("AVISO: o config do dono ja tem bloco clarify: — nao mexi nele")
        # IA SÓ DO BONECO (ver _ia_do_boneco): modelo no config e chave num .env PRÓPRIO
        # do espelho (no lugar do hardlink do .env do dono, que o passo 1 acabou de refazer).
        # ⚠️ Texto puro, como o .env do próprio Hermes — e só na pasta do PetAgent.
        ia = _ia_do_boneco()
        if ia:
            limpo = _troca_modelo(limpo, ia)
            env_espelho = os.path.join(HOME_ESPELHO, ".env")
            if os.path.lexists(env_espelho):
                os.remove(env_espelho)   # é o hardlink do .env do dono: SÓ o nome sai daqui
            with open(env_espelho, "w", encoding="utf-8") as fh:
                fh.write(_env_do_espelho(real, ia))
            log(f"config do backend: IA só do boneco = {ia['provedor']} / {ia['modelo']}")
        with open(os.path.join(HOME_ESPELHO, "config.yaml"), "w", encoding="utf-8") as fh:
            fh.write(limpo)
        removidos = len(bruto) - len(limpo)
        log("home espelhado pronto (%s): config.yaml sem %s (%d bytes a menos)"
            % (HOME_ESPELHO, ", ".join(MCP_A_REMOVER), removidos))
    except Exception as e:  # noqa: BLE001
        # degradado, mas não quebra o chat: segue com o home real (e o overlay volta)
        log("home espelhado FALHOU (%r) — seguindo com o home real" % (e,))
        return ""
    return HOME_ESPELHO


def _env_do_backend(token: str) -> dict:
    """Ambiente do backend próprio.

    PYTHONPATH SAI: herdado, ele põe `hermes-agent\\venv\\Lib\\site-packages` antes
    do site-packages do venv escolhido, e aí o fastapi velho do venv principal
    sombreia o fastapi correto do outro venv (um `import fastapi` ainda "existe",
    então ninguém percebe — só quebra na hora de construir o app).

    HERMES_HOME vai para o HOME ESPELHADO: herda skills/plugins/memórias/sessões/.env
    do dono por junction+hardlink, mas o config.yaml é nosso — sem o MCP `cua-driver`,
    que abria uma janela de 3840x1440 em cima de tudo (ver _home_do_backend).

    HERMES_TUI_MAX_TURNS é o teto de iterações da IA por mensagem: no `hermes serve` a
    variável VENCE o `agent.max_turns` do config (tui_gateway/agent_callbacks.py) — vale
    mesmo se o espelho falhar e o backend subir com o config do dono.
    """
    env = dict(os.environ)
    env.pop("PYTHONPATH", None)
    env["HERMES_TUI_MAX_TURNS"] = str(MAX_ITERACOES_IA)
    env.pop("PETAGENT_IA_CHAVE", None)   # a chave já está no .env do espelho; o backend não precisa dela solta
    espelho = _home_do_backend()
    if espelho:
        env["HERMES_HOME"] = espelho
    env["HERMES_DASHBOARD_SESSION_TOKEN"] = token  # token local, meu
    env["PYTHONIOENCODING"] = "utf-8"
    return env


def _motivo_backend() -> str:
    """Última linha do backend.log = o erro de verdade (a última linha de um traceback)."""
    try:
        with open(os.path.join(AQUI, "backend.log"), "rb") as fh:
            linhas = [l for l in fh.read().splitlines() if l.strip()]
        return linhas[-1].decode("utf-8", "replace")[:300] if linhas else "(backend.log vazio)"
    except Exception:  # noqa: BLE001
        return "(não consegui ler o backend.log)"


CWD_PADRAO = os.path.expanduser("~")
PORTA_PADRAO = int(os.environ.get("PETAGENT_PORTA_BACKEND", "9131"))

# Windows + stdio em PIPE: sem isto, qualquer acento derruba a ponte (02/10/2026)
for _fluxo in ("stdin", "stdout", "stderr"):
    try:
        getattr(sys, _fluxo).reconfigure(encoding="utf-8", errors="replace")
    except Exception:  # noqa: BLE001
        pass

ARQ_LOG = os.path.join(os.environ.get("LOCALAPPDATA", AQUI), "petagent", "ponte.log")

# eventos já conhecidos do backend (não precisam virar log; os NOVOS viram)
_EV_CONHECIDOS = {
    "gateway.ready", "session.info", "sessions.changed", "reasoning.available",
    "thinking.delta", "message.start", "message.delta", "message.complete",
    "tool.start", "tool.complete", "tool.delta",
    # Medido 10/10/2026: estes NÃO estavam na lista e cada token virava uma linha no
    # ponte.log — 3.802 linhas num único turno de 224 s (e o disco pagando por isso).
    # "reasoning.delta" é o pensamento token a token; não serve para diagnosticar nada.
    "reasoning.delta", "tool.generating", "message.interim",
    "error",  # tem log próprio e completo em _perguntar_uma (o genérico cortava em 200)
    "request.cancel",  # o Hermes retirou uma pergunta (aprovação): tratado em _perguntar_uma
}

# Aprovações: as escolhas que o Hermes aceita (contracts/server_requests.py::ApprovalChoice)
ESCOLHAS_APROVACAO = ("once", "session", "always", "deny")
# Pedido servidor→cliente que a ponte não sabe responder: este erro faz o Hermes desistir NA HORA
# (o contrato de client.capabilities pede -32601). Calado, o agente esperaria o prazo inteiro.
SEM_TRATAMENTO = -32601


def log(msg: str) -> None:
    linha = f"[ponte] {msg}"
    try:
        os.makedirs(os.path.dirname(ARQ_LOG), exist_ok=True)
        with open(ARQ_LOG, "a", encoding="utf-8") as fh:
            fh.write(time.strftime("%d/%m %H:%M:%S ") + linha + "\n")
    except OSError:
        pass
    try:
        print(linha, file=sys.stderr, flush=True)
    except Exception:  # noqa: BLE001
        pass


# ---------------------------------------------------------------------------
# Progresso em tempo real (10/10/2026)
#
# O dono: "eu não fico nunca no vácuo aqui no chat". Medido: no Telegram CADA passo do agente
# vira uma mensagem (11 ferramentas = 11 mensagens recebidas); no boneco eram 9 ferramentas
# virando UMA mensagem no fim — 224 s olhando um balão parado. Aqui a ponte emite o que está
# acontecendo enquanto acontece, e o app repassa para a tela.
# ---------------------------------------------------------------------------
EM_TESTE = False            # --teste espera UM json no stdout: não emitir progresso
_pedido_atual = None        # id do pedido em andamento: vai em cada linha (o app descarta o de outro)
_ultimo_progresso = 0.0
_INTERVALO_PROGRESSO = 0.5  # freio: sem isto, cada token viraria uma linha (3.802 num turno)


def _progresso(tipo: str, texto: str) -> None:
    """Emite uma linha de progresso para o app (não é a resposta). Com freio de tempo."""
    global _ultimo_progresso
    if EM_TESTE:
        return
    agora = time.time()
    # ⚠️ O freio vale SÓ para texto: se o "passo" (ex.: "pensando") resetasse o relógio, o
    # primeiro trecho da resposta caía no freio e não aparecia — medido em 10/10/2026.
    if tipo == "texto":
        if agora - _ultimo_progresso < _INTERVALO_PROGRESSO:
            return
        _ultimo_progresso = agora
    try:
        print(json.dumps({"ok": True, "id": _pedido_atual, "progresso": tipo,
                          "texto": str(texto)[:800]}, ensure_ascii=False), flush=True)
    except Exception:  # noqa: BLE001
        pass


def _avisa_aprovacao(dados: dict) -> None:
    """Leva ao app a pergunta de aprovação (o cartão do chat). Linha própria, sem o freio nem o
    corte de 800 do _progresso: o JSON cortado no meio chegaria ilegível."""
    if EM_TESTE:
        return
    try:
        print(json.dumps({"ok": True, "id": _pedido_atual, "progresso": "aprovacao",
                          "aprovacao": dados}, ensure_ascii=False), flush=True)
    except Exception:  # noqa: BLE001
        pass


def porta_aberta(porta: int) -> bool:
    try:
        with socket.create_connection(("127.0.0.1", porta), timeout=2):
            return True
    except OSError:
        return False


def _derruba_arvore(proc) -> None:
    """Derruba o backend E toda a árvore de processos dele (conserto de 13/10/2026).

    No Hermes 0.21 o `hermes serve` é uma CADEIA — `bin\\hermes.exe` (lançador) → python
    — e quem escuta a porta é o python. `proc.terminate()` no Windows só mata o processo
    do topo; quase sempre o lançador leva o filho junto, mas às vezes o serve se
    re-executa (`python -I -I -c ...runpy`) e esse NETO escapa. Medido em 13/10/2026: o
    app fechou e o neto seguiu vivo, escutando a porta antiga. Pior que memória: o 0.21
    RECUSA subir outro serve no mesmo home enquanto o órfão vive ("this host is already
    served by PID ...", código 78) — a reconexão e a próxima subida do app falhavam e o
    chat caía na reserva lenta. O `taskkill /T` leva a árvore inteira, qualquer que seja
    o formato (CREATE_NO_WINDOW: nada de console piscando).
    """
    if proc is None or proc.poll() is not None:
        return
    try:
        if os.name == "nt":
            subprocess.run(["taskkill", "/PID", str(proc.pid), "/T", "/F"],
                           capture_output=True, timeout=15,
                           creationflags=subprocess.CREATE_NO_WINDOW)
        else:
            proc.terminate()
        proc.wait(timeout=10)
    except Exception:  # noqa: BLE001
        try:
            proc.kill()
        except Exception:  # noqa: BLE001
            pass


class Backend:
    """O Hermes do boneco: processo próprio (`hermes serve`) + canal /api/ws."""

    def __init__(self, porta: int) -> None:
        self.porta = porta
        self.token = os.environ.get("PETAGENT_TOKEN_BACKEND") or secrets.token_urlsafe(24)
        self.proc: subprocess.Popen | None = None
        self.ws = None
        self.sessao = ""      # id de EXECUÇÃO da conversa atual (vale enquanto o serve vive)
        self.chave = ""       # id GUARDADO da conversa atual (o que o app conhece)
        self.vivas: dict[str, str] = {}   # guardado -> execução, das conversas já abertas aqui
        self.modelo = ""
        self.seq = 0
        self.aprovacoes: set[str] = set()  # ids "srq-…" das aprovações esperando a dona

    # ---------- subida ----------
    def subir_processo(self) -> bool:
        if not os.path.exists(HERMES_CMD[0]):
            log(f"hermes não encontrado em {HERMES_CMD[0]}")
            return False
        env = _env_do_backend(self.token)
        cmd = HERMES_CMD + ["serve", "--port", str(self.porta), "--host", "127.0.0.1", "--skip-build"]
        log(f"backend: {' '.join(HERMES_CMD)} serve (PYTHONPATH herdado fora)")
        kwargs: dict = {}
        if os.name == "nt":
            si = subprocess.STARTUPINFO()
            si.dwFlags |= subprocess.STARTF_USESHOWWINDOW
            si.wShowWindow = 0  # SW_HIDE: console oculto herdado, nada pipoca
            kwargs["startupinfo"] = si
        try:
            arq = open(os.path.join(AQUI, "backend.log"), "ab", buffering=0)
        except OSError:
            arq = subprocess.DEVNULL  # type: ignore[assignment]
        self.proc = subprocess.Popen(
            cmd,
            env=env, stdout=arq, stderr=arq, cwd=CWD_PADRAO, **kwargs,
        )
        t0 = time.time()
        while time.time() - t0 < 200:
            if porta_aberta(self.porta):
                log(f"backend próprio no ar em {time.time() - t0:.1f}s (porta {self.porta})")
                return True
            if self.proc.poll() is not None:
                # o motivo TEM de aparecer aqui: antes era só "backend não subiu" e
                # ninguém sabia que o hermes serve tinha morrido no import (03/10/2026)
                log(f"backend morreu ao subir (código {self.proc.returncode}) — motivo: {_motivo_backend()}")
                return False
            time.sleep(1.5)
        log("backend não subiu a tempo")
        return False

    async def rpc(self, metodo: str, params: dict, timeout: float = 240) -> dict:
        self.seq += 1
        meu = f"{metodo}#{self.seq}"
        await self.ws.send(json.dumps({"jsonrpc": "2.0", "id": meu, "method": metodo,
                                       "params": params}))
        limite = time.time() + timeout
        while time.time() < limite:
            raw = await asyncio.wait_for(self.ws.recv(), timeout=max(1, limite - time.time()))
            try:
                d = json.loads(raw)
            except (json.JSONDecodeError, TypeError):
                continue
            if not isinstance(d, dict):
                continue
            if str(d.get("id") or "") == meu:
                return d
            if await self._pedido_do_servidor(d):
                continue
            # eventos do gateway chegam antes: só registra os interessantes
            p = d.get("params") if isinstance(d.get("params"), dict) else {}
            if str(p.get("type") or "") not in ("", "gateway.ready"):
                log(f"evento: {str(p.get('type'))[:40]}")
        return {"erro": "tempo esgotado"}

    async def conectar(self) -> bool:
        url = f"ws://127.0.0.1:{self.porta}/api/ws?token={self.token}"
        self.ws = await websockets.connect(url, open_timeout=30, max_size=32 << 20)
        # a conversa a retomar: a que estava aberta antes da queda (reconexão) ou, na subida,
        # a última que a dona usou (o app passa em PETAGENT_CONVERSA). Antes, toda subida
        # criava sessão nova e o boneco esquecia a conversa ao reiniciar o app.
        alvo = self.chave or os.environ.get("PETAGENT_CONVERSA", "").strip()
        self.vivas = {}   # backend novo: nenhuma sessão de execução antiga vale mais
        self.aprovacoes = set()
        # avisa que esta ponte RESPONDE pedidos servidor→cliente (as aprovações; ver o topo do
        # arquivo). Vale por conexão: toda reconexão manda de novo. Falhar não impede o chat —
        # só as aprovações voltam a ser recusadas.
        try:
            r = await self.rpc("client.capabilities", {"server_requests": True}, timeout=30)
            if "error" in r or "erro" in r:
                log(f"client.capabilities recusado: {json.dumps(r)[:200]}")
        except Exception as e:  # noqa: BLE001
            log(f"client.capabilities falhou ({type(e).__name__}: {e}) — aprovações indisponíveis")
        r = await self.rpc("session.create", {"cwd": CWD_PADRAO, "source": "petagent"}, timeout=240)
        res = r.get("result") if isinstance(r.get("result"), dict) else {}
        self.sessao = str(res.get("session_id") or "")
        if not self.sessao:
            log(f"session.create falhou: {json.dumps(r)[:200]}")
            return False
        self.chave = str(res.get("stored_session_id") or "")
        if self.chave:
            self.vivas[self.chave] = self.sessao
        info = res.get("info") if isinstance(res.get("info"), dict) else {}
        self.modelo = str(info.get('model') or info.get('model_name') or "")
        log(f"sessão {self.sessao} criada · modelo={self.modelo} · cwd={info.get('cwd')}")
        if alvo and alvo != self.chave:
            try:
                if await self.usar_conversa(alvo):
                    log(f"conversa {alvo[:12]} retomada")
            except Exception as e:  # noqa: BLE001
                log(f"não consegui retomar a conversa {alvo[:12]} ({type(e).__name__}: {e}) — segue a nova")
        return True

    # ---------- conversas ----------
    async def usar_conversa(self, chave: str) -> bool:
        """Passa a falar na conversa `chave` (id guardado). Vazio ou a atual: nada a fazer."""
        chave = (chave or "").strip()
        if not chave or chave == self.chave:
            return True
        sid = self.vivas.get(chave, "")
        if not sid:
            r = await self.rpc("session.resume", {"session_id": chave, "omit_messages": True,
                                                  "source": "petagent"}, timeout=120)
            dados = _dados_do_rpc(r) or {}
            sid = str(dados.get("session_id") or "")
            if not sid:
                log(f"session.resume {chave[:12]} falhou: {json.dumps(r)[:200]}")
                return False
            self.vivas[chave] = sid
        self.sessao, self.chave = sid, chave
        return True

    async def operacao(self, pedido: dict) -> dict:
        """As operações de conversa do app ({"op": ...}). Teto de 90 s, como os comandos:
        o laço da ponte é serial e uma operação pendurada seguraria o chat."""
        op = str(pedido.get("op") or "")
        try:
            return await asyncio.wait_for(self._operacao_impl(op, pedido), timeout=90)
        except asyncio.TimeoutError:
            log(f"op {op} ESTOUROU 90s")
            return {"erro": f"A operação ({op}) demorou demais. Tente novamente."}

    async def _operacao_impl(self, op: str, pedido: dict) -> dict:
        conversa = str(pedido.get("conversa") or "").strip()
        if op == "listar":
            r = await self.rpc("session.list", {"limit": 300}, timeout=60)
            linhas = (_dados_do_rpc(r) or {}).get("sessions") or []
            lista = []
            for s in linhas:
                if not isinstance(s, dict) or not s.get("id"):
                    continue
                previa = re.sub(r"\s*@file:\S+", "", _sem_contexto_anexado(s.get("preview") or "")).strip()
                lista.append({"id": s.get("id"), "titulo": s.get("title") or "",
                              "previa": previa, "inicio": s.get("started_at") or 0,
                              "mensagens": s.get("message_count") or 0})
            return {"conversas": lista, "atual": self.chave}

        if op == "nova":
            if not await self._nova_sessao():
                return {"erro": "Não foi possível criar uma nova conversa."}
            return {"atual": self.chave}

        if op == "abrir":
            # resume COM as mensagens e sem as imagens embutidas (senão cada foto antiga viria
            # de volta em base64 — megabytes por conversa)
            r = await self.rpc("session.resume", {"session_id": conversa, "inline_images": False,
                                                  "source": "petagent"}, timeout=120)
            dados = _dados_do_rpc(r) or {}
            sid = str(dados.get("session_id") or "")
            if not sid:
                return {"erro": "Não foi possível abrir esta conversa."}
            self.vivas[conversa] = sid
            self.sessao, self.chave = sid, conversa
            msgs = []
            for m in dados.get("messages") or []:
                if not isinstance(m, dict) or m.get("role") not in ("user", "assistant"):
                    continue
                t = _sem_contexto_anexado(str(m.get("text") or ""))
                t = re.sub(r"data:image/[^\s)\"']+", "[imagem]", t)
                if t.strip():
                    msgs.append({"quem": "eu" if m["role"] == "user" else "ele", "texto": t,
                                 "quando": m.get("timestamp") or 0})
            return {"atual": self.chave, "mensagens": msgs}

        if op == "renomear":
            titulo = str(pedido.get("titulo") or "").strip()
            if not titulo:
                return {"erro": "Dê um nome para a conversa."}
            sid = self.vivas.get(conversa, "")
            if not sid:   # resume preguiçoso: só para ter o id de execução, sem montar agente
                r = await self.rpc("session.resume", {"session_id": conversa, "lazy": True,
                                                      "omit_messages": True, "source": "petagent"},
                                   timeout=60)
                sid = str((_dados_do_rpc(r) or {}).get("session_id") or "")
                if not sid:
                    return {"erro": "Esta conversa não foi encontrada."}
                self.vivas[conversa] = sid
            r = await self.rpc("session.title", {"session_id": sid, "title": titulo}, timeout=30)
            if "error" in r:
                return {"erro": str((r.get("error") or {}).get("message") or r.get("error"))[:200]}
            return {"titulo": titulo}

        if op == "excluir":
            if not conversa:
                return {"erro": "Nenhuma conversa foi indicada."}
            # o Hermes recusa apagar sessão viva: a atual dá lugar a uma nova, e a de execução
            # que já tenha sido aberta aqui é fechada antes
            if conversa == self.chave and not await self._nova_sessao():
                return {"erro": "Não foi possível sair desta conversa para excluí-la."}
            sid = self.vivas.pop(conversa, "")
            if sid:
                try:
                    await self.rpc("session.close", {"session_id": sid}, timeout=30)
                except Exception as e:  # noqa: BLE001
                    log(f"session.close {sid[:12]} falhou ({type(e).__name__})")
            r = await self.rpc("session.delete", {"session_id": conversa}, timeout=30)
            if "error" in r:
                msg = str((r.get("error") or {}).get("message") or r.get("error"))
                # nunca chegou a ter mensagem: não há linha no banco, e está tudo certo
                if "not found" not in msg:
                    return {"erro": msg[:200]}
            return {"atual": self.chave}

        return {"erro": f"Operação desconhecida: {op}."}

    async def subir(self) -> bool:
        if not porta_aberta(self.porta):
            if not self.subir_processo():
                return False
        else:
            log("já havia backend nesta porta (reusando)")
        try:
            return await self.conectar()
        except Exception as e:  # noqa: BLE001
            log(f"canal falhou: {type(e).__name__}: {e}")
            # backend que já estava na porta mas não entrega canal (travado/órfão):
            # antes a ponte só registrava "reusando" e seguia com um serve surdo (05/10)
            return await self.reconectar(f"subida inicial: {type(e).__name__}")

    # ---------- reconexão (conserto de 05/10/2026) ----------
    async def _fechar_canal(self) -> None:
        try:
            if self.ws is not None:
                await asyncio.wait_for(self.ws.close(), timeout=5)
        except Exception:  # noqa: BLE001
            pass
        self.ws = None

    async def reconectar(self, motivo: str = "") -> bool:
        """Revive o trilho quente quando o canal morre.

        Antes (incidente 05/10/2026 17:36): o WebSocket caía por `keepalive ping
        timeout`, a ponte ia para a reserva lenta (`hermes -z`, 2+ min por resposta) e
        ficava lá ATÉ ALGUÉM REINICIAR O APP. Agora: fecha o canal morto, derruba o
        backend que parou de responder (um `hermes serve` travado não volta sozinho) e
        sobe/conecta de novo, criando sessão nova.
        """
        log(f"canal caiu ({motivo or 'sem detalhe'}) — reconectando")
        await self._fechar_canal()
        _derruba_arvore(self.proc)  # a árvore toda: o serve de verdade é neto do lançador
        # espera a porta soltar; se algum processo alheio/travado a segurar, pega outra
        t0 = time.time()
        while porta_aberta(self.porta) and time.time() - t0 < 20:
            await asyncio.sleep(1)
        if porta_aberta(self.porta):
            self.porta = porta_livre()
            log(f"porta antiga ainda ocupada — passando para {self.porta}")
        if not self.subir_processo():
            log("reconexão: não consegui subir o backend de novo")
            return False
        try:
            if not await self.conectar():
                return False
        except Exception as e:  # noqa: BLE001
            log(f"reconexão falhou no canal: {type(e).__name__}: {e}")
            return False
        log("canal quente reconectado")
        return True

    # ---------- aprovações (30/10/2026; ver o topo do arquivo) ----------
    async def _pedido_do_servidor(self, d: dict) -> bool:
        """Trata um pedido servidor→cliente (`{"id": "srq-…", "method": ...}`). True = era um.

        `approval` vira o cartão do chat. Qualquer outro (clarify, sudo, secret, os da janela do
        Desktop…) a ponte não sabe mostrar: responde -32601 na hora e o Hermes segue sem esperar
        — calado, o agente ficaria parado até o prazo (300 s no clarify)."""
        rid, metodo = d.get("id"), d.get("method")
        if not isinstance(rid, str) or not isinstance(metodo, str) or metodo == "event":
            return False
        p = d.get("params") if isinstance(d.get("params"), dict) else {}
        if metodo == "approval":
            self.aprovacoes.add(rid)
            opcoes = [c for c in (p.get("choices") or []) if c in ESCOLHAS_APROVACAO] or ["once", "deny"]
            log(f"aprovação {rid} pedida ({p.get('tool_name') or 'ferramenta'}): "
                f"{str(p.get('description') or '')[:120]}")
            _avisa_aprovacao({"pedido": rid, "comando": str(p.get("command") or "")[:4000],
                              "descricao": str(p.get("description") or ""),
                              "ferramenta": str(p.get("tool_name") or ""), "opcoes": opcoes})
            return True
        log(f"pedido do servidor sem tratamento aqui: {metodo} — recusado (-32601)")
        await self._envia({"jsonrpc": "2.0", "id": rid,
                           "error": {"code": SEM_TRATAMENTO,
                                     "message": f"o chat do PetAgent não responde '{metodo}'"}})
        return True

    def _aprovacao_retirada(self, p: dict) -> None:
        """`request.cancel`: o Hermes desistiu da pergunta (prazo, interrupção). O cartão sai da tela."""
        carga = p.get("payload") if isinstance(p.get("payload"), dict) else p
        rid = str(carga.get("id") or "")
        if rid in self.aprovacoes:
            self.aprovacoes.discard(rid)
            log(f"aprovação {rid} retirada pelo Hermes ({carga.get('reason')})")
            _progresso("aprovacao-fim", rid)

    async def responde_aprovacao(self, rid: str, escolha: str) -> None:
        """A escolha da dona no cartão (chega pelo stdin, no meio da pergunta — ver _le_stdin)."""
        if escolha not in ESCOLHAS_APROVACAO:
            log(f"aprovação {rid}: escolha inválida {escolha!r} — tratada como negar")
            escolha = "deny"
        if rid not in self.aprovacoes:
            log(f"aprovação {rid}: já não estava aberta (o Hermes retirou ou a ponte reconectou)")
            return
        self.aprovacoes.discard(rid)
        log(f"aprovação {rid}: {escolha}")
        await self._envia({"jsonrpc": "2.0", "id": rid, "result": {"choice": escolha}})

    async def _envia(self, frame: dict) -> None:
        if self.ws is None:
            log("canal fechado: resposta ao Hermes não enviada")
            return
        try:
            await self.ws.send(json.dumps(frame))
        except Exception as e:  # noqa: BLE001
            log(f"falha ao responder o Hermes ({type(e).__name__}: {e})")

    # ---------- perguntar ----------
    async def perguntar(self, texto: str, timeout: float = 300,
                        anexos: list | None = None) -> tuple[str, float]:
        """Pergunta com uma rede de segurança: se o canal estiver morto, reconecta
        UMA vez e repete — em vez de devolver erro e o app cair na reserva lenta."""
        # COMANDOS DE BARRA (pedido do dono, 10/10/2026): o chat do boneco passa a aceitar
        # /new, /compact, /reset e /status — e, por encaminhamento ao `slash.exec`, todos os
        # demais comandos que o Hermes já tem. Sem isto, digitar "/new" ia como TEXTO para o
        # modelo, que respondia sobre a barra em vez de executar o comando.
        if texto.strip().startswith("/") and not anexos:
            return await self._comando(texto.strip())

        # INSTRUMENTAÇÃO (10/10/2026): o dono mediu 205 s numa pergunta simples e o log só
        # mostrava "tempo esgotado (150s)" DO LADO DO APP — que desistia antes da ponte. Sem
        # registrar o que acontece DURANTE a espera, a causa primeira fica invisível e eu
        # passei a chutar hipóteses (cheguei a medir tamanho de sessão errada). Este vigia
        # apenas ESCREVE NO LOG: não muda comportamento nem prazo.
        vigia = asyncio.create_task(self._vigiaDeDemora())
        t0 = time.time()
        try:
            try:
                resp = await self._perguntar_uma(texto, timeout, anexos)
            except (websockets.exceptions.WebSocketException, OSError) as e:
                log(f"pergunta falhou por canal morto ({type(e).__name__}: {e})")
                if not await self.reconectar(type(e).__name__):
                    raise
                resp = await self._perguntar_uma(texto, timeout, anexos)
            log(f"pergunta respondida em {time.time() - t0:.1f}s ({len(resp[0])} chars)")
            return resp
        finally:
            vigia.cancel()

    async def _comando(self, texto: str) -> tuple[str, float]:
        """Executa um comando de barra do chat do boneco.

        ⚠️ TETO DURO (10/10/2026): o laço da ponte é SERIAL — um comando pendurado faz o
        PRÓXIMO esperar. Foi o que o dono viu: o `/help` travou no `commands.catalog` (uma
        chamada de rede) e o `/clear` digitado depois ficou ~2 min na fila, atrás dele. Agora
        TODO comando tem teto de 90 s e devolve erro em vez de segurar o chat.
        """
        t0 = time.time()
        nome = texto.split()[0].lower()
        log(f"comando: {nome}")
        try:
            return await asyncio.wait_for(self._comando_impl(texto, nome), timeout=90)
        except asyncio.TimeoutError:
            log(f"comando: {nome} ESTOUROU 90s — interrompido para nao travar a fila")
            return (f"o comando `{nome}` demorou mais de 90 s e eu o interrompi, "
                    "para não travar os próximos.\n(Detalhe no ponte.log.)"), time.time() - t0

    async def _comando_impl(self, texto: str, nome: str) -> tuple[str, float]:
        """O corpo dos comandos (chamado sob o teto de 90 s de `_comando`)."""
        t0 = time.time()

        if nome in ("/new", "/nova", "/reset", "/clear", "/limpar"):
            anterior = self.sessao
            ok = await self._nova_sessao()
            if not ok:
                return "Não foi possível criar uma nova sessão. Veja os detalhes no ponte.log.", time.time() - t0
            # Pedido do dono (10/10/2026): `/clear` limpa o CHAT e o contexto; `/new` também
            # pode limpar o chat. A tela limpa sozinha ao ver estes comandos (main.js,
            # `enviarTexto`) — a ponte só avisa o que aconteceu com o contexto.
            if nome in ("/reset", "/clear", "/limpar"):
                return ("Chat limpo e contexto zerado.\n\n"
                        "• Sessão nova: `%s`\n• Sessão anterior: `%s`\n\n"
                        "O agente começa sem lembrar desta conversa. As memórias e a personalidade "
                        "continuam, pois pertencem ao perfil, e não à sessão."
                        % (self.sessao[:8], str(anterior)[:8])), time.time() - t0
            return ("Nova sessão criada e chat limpo.\n\n"
                    "• Sessão: `%s`\n• Sessão anterior: `%s`\n\n"
                    "A conversa anterior saiu do contexto; as memórias e a personalidade continuam."
                    % (self.sessao[:8], str(anterior)[:8])), time.time() - t0

        if nome in ("/help", "/ajuda", "/comandos"):
            return await self._ajuda(), time.time() - t0

        if nome in ("/status", "/usage", "/contexto"):
            return await self._status(), time.time() - t0

        # Qualquer outro comando: o Hermes executa (é o mesmo catálogo do CLI/Telegram).
        try:
            r = await self.rpc("slash.exec", {"session_id": self.sessao, "command": texto},
                               timeout=240)
            saida = _texto_do_rpc(r)
            if not saida:
                saida = (f"O comando `{nome}` foi executado, mas não devolveu nenhum texto.\n"
                         "(Se era /compact, veja os detalhes no ponte.log.)")
            return saida, time.time() - t0
        except Exception as e:  # noqa: BLE001
            return (f"Não foi possível executar `{nome}`: {type(e).__name__}: {e}\n\n"
                    "Comandos tratados pela própria ponte: /new, /reset e /status."
                    ), time.time() - t0

    async def _ajuda(self) -> str:
        """Lista os comandos de barra (pedido do dono, 10/10/2026).

        ⚠️ DEFEITO CORRIGIDO no mesmo dia: a primeira versão chamava `commands.catalog` e
        essa chamada PENDUROU no backend do app (funcionava só com o backend recém-subido).
        Como o laço da ponte é serial, o `/help` travado fez o `/clear` seguinte esperar ~2 min.
        Agora a lista do boneco é ESTÁTICA (sempre instantânea) e o catálogo do Hermes é só um
        extra, com prazo CURTO: se não vier em 8 s, a resposta sai sem ele.
        """
        base = """COMANDOS DO CHAT

Comandos deste chat (sempre disponíveis):
  /new      cria uma sessão nova (limpa o chat e o contexto)
  /clear    limpa o chat e o contexto
  /reset    zera a sessão e o chat
  /status   mostra o modelo em uso, a sessão, as chamadas e o tamanho do contexto
  /compact  compacta o contexto (resume o histórico)
  /help     mostra esta lista"""
        try:
            r = await self.rpc("commands.catalog", {}, timeout=8)
            pares = _paresDoCatalogo(r)
        except Exception as e:  # noqa: BLE001
            log(f"catalogo do Hermes nao veio a tempo ({type(e).__name__})")
            pares = []
        if pares:
            base += "\n\nComandos do Hermes (o boneco também os executa):"
            base += "".join(f"\n  /{n.lstrip('/')}" + (f"  {d}" if d else "")
                            for n, d in pares[:60])
        else:
            base += ("\n\nComandos do Hermes: o boneco executa os mesmos comandos do CLI e do Telegram "
                     "(/new, /clear, /compact, /history, /save, /retry, …). Ao digitar um comando "
                     "desconhecido, o próprio Hermes mostra a lista completa.")
        return base

    async def _status(self) -> str:
        """O que o dono pediu no /status: TAMANHO DO CONTEXTO e a LLM em uso."""
        linhas = ["STATUS DA SESSÃO", ""]
        linhas.append(f"• Modelo: {self.modelo or '(não informado)'}")
        linhas.append(f"• Sessão: {self.sessao[:8] or '(sem sessão)'}")
        try:
            u = await self.rpc("session.usage", {"session_id": self.sessao}, timeout=30)
            uso = _dados_do_rpc(u) or {}
            ent = uso.get("input") or 0
            sai = uso.get("output") or 0
            tot = uso.get("total") or (ent + sai)
            chamadas = uso.get("calls") or 0
            linhas.append(f"• Chamadas nesta sessão: {chamadas}")
            linhas.append(f"• Tokens: entrada {ent:,} · saída {sai:,} · total {tot:,}".replace(",", "."))
            if ent:
                linhas.append("")
                linhas.append(_barraDeContexto(ent))
        except Exception as e:  # noqa: BLE001
            linhas.append(f"• Uso: não foi possível medir ({type(e).__name__})")
        linhas.append("")
        linhas.append("Comandos: /new (sessão nova) · /reset (zerar) · /compact (compactar) · /status")
        return "\n".join(linhas)

    async def _nova_sessao(self) -> bool:
        """Cria uma sessão nova no backend e passa a usá-la."""
        try:
            r = await self.rpc("session.create", {"cwd": CWD_PADRAO, "cols": 80,
                                                  "source": "petagent"}, timeout=60)
        except Exception as e:  # noqa: BLE001
            log(f"session.create falhou no comando: {type(e).__name__}: {e}")
            return False
        dados = _dados_do_rpc(r) or {}
        sid = str(dados.get("session_id") or dados.get("id") or "")
        if not sid:
            log(f"session.create sem id: {json.dumps(r)[:200]}")
            return False
        self.sessao = sid
        self.chave = str(dados.get("stored_session_id") or "")
        if self.chave:
            self.vivas[self.chave] = sid
        log(f"comando: sessão nova {sid}")
        return True

    async def _vigiaDeDemora(self):
        """Marca no log o canal quente ficando lento. Só observa — não interfere."""
        try:
            await asyncio.sleep(45)
            log("canal quente: 45s sem resposta (fila de turno anterior? canal morto?)")
            await asyncio.sleep(60)
            log("canal quente: 105s sem resposta — se nada voltar, a reserva e' o sintoma")
        except asyncio.CancelledError:
            raise

    async def _anexar(self, anexos: list) -> tuple[list[str], str]:
        """Põe os anexos na fila da sessão. Devolve (refs @file: para a mensagem, erro)."""
        refs: list[str] = []
        for a in anexos:
            if not isinstance(a, dict):
                continue
            nome = str(a.get("nome") or "anexo")
            b64 = str(a.get("base64") or "")
            _progresso("passo", f"Anexando {nome}")
            if a.get("tipo") == "imagem":
                r = await self.rpc("image.attach_bytes", {"session_id": self.sessao,
                                                          "content_base64": b64, "filename": nome},
                                   timeout=60)
            else:
                r = await self.rpc("file.attach", {"session_id": self.sessao, "name": nome,
                                                   "data_url": "data:text/plain;base64," + b64},
                                   timeout=60)
            if "error" in r:
                msg = str((r.get("error") or {}).get("message") or r.get("error"))
                log(f"anexo {nome} recusado: {msg[:300]}")
                return refs, f"o Hermes recusou o anexo {nome}: {msg[:200]}"
            ref = str((_dados_do_rpc(r) or {}).get("ref_text") or "")
            if ref:
                refs.append(ref)
            log(f"anexo {nome} na fila ({a.get('tipo')}, {len(b64) * 3 // 4} bytes)")
        return refs, ""

    async def _perguntar_uma(self, texto: str, timeout: float = 300,
                             anexos: list | None = None) -> tuple[str, float]:
        t0 = time.time()
        # AVISO IMEDIATO (10/10/2026): medido — o modelo passa o tempo PENSANDO e o texto sai
        # em rajada no fim, então só transmitir o texto deixaria o balão parado de novo. Este
        # primeiro aviso garante que a tela mostra movimento desde o primeiro instante.
        _progresso("passo", "Pensando")
        if anexos:
            refs, erro = await self._anexar(anexos)
            if erro:
                return erro, time.time() - t0
            if refs:
                texto = texto + "\n\n" + "\n".join(refs)
            _progresso("passo", "Pensando")
        await self.ws.send(json.dumps({"jsonrpc": "2.0", "id": f"p{self.seq + 1}",
                                       "method": "prompt.submit",
                                       "params": {"session_id": self.sessao, "text": texto}}))
        self.seq += 1
        pedacos: list[str] = []
        erro_hermes = ""  # evento `error` do turno (ver _erro_legivel)
        limite = t0 + timeout
        while time.time() < limite:
            try:
                raw = await asyncio.wait_for(self.ws.recv(), timeout=max(1, limite - time.time()))
            except asyncio.TimeoutError:
                return "".join(pedacos).strip() or "(tempo esgotado)", time.time() - t0
            try:
                d = json.loads(raw)
            except (json.JSONDecodeError, TypeError):
                continue
            if not isinstance(d, dict):
                continue
            if await self._pedido_do_servidor(d):
                # o tempo que a dona leva para decidir não conta contra a resposta: o prazo
                # recomeça daqui (o próprio Hermes desiste da pergunta em 300 s)
                limite = max(limite, time.time() + timeout)
                continue
            p = d.get("params") if isinstance(d.get("params"), dict) else {}
            tipo = str(p.get("type") or d.get("method") or "")
            if tipo and tipo not in _EV_CONHECIDOS:
                log(f"ev novo={tipo} chaves={sorted(p.keys())} "
                    f"payload={json.dumps(p.get('payload'), ensure_ascii=False)[:200]}")
            if tipo == "message.delta":
                carga = p.get("payload") if isinstance(p.get("payload"), dict) else p
                txt = carga.get("text") or carga.get("delta")
                if isinstance(txt, dict):
                    txt = txt.get("text")
                if isinstance(txt, str):
                    pedacos.append(txt)
                    _progresso("texto", "".join(pedacos))
            elif tipo == "message.interim":
                carga = p.get("payload") if isinstance(p.get("payload"), dict) else p
                t = carga.get("text")
                if isinstance(t, str) and t.strip():
                    _progresso("texto", t)
            elif tipo in ("tool.generating", "tool.start"):
                carga = p.get("payload") if isinstance(p.get("payload"), dict) else p
                nome = str(carga.get("name") or carga.get("tool") or "ferramenta")
                _progresso("passo", nome)
            elif tipo == "error":
                carga = p.get("payload") if isinstance(p.get("payload"), dict) else p
                erro_hermes = str(carga.get("message") or carga.get("error") or carga)
                log(f"erro do Hermes no turno: {erro_hermes[:1500]}")
            elif tipo == "request.cancel":
                self._aprovacao_retirada(p)
            elif tipo == "message.complete":
                break
            # thinking.delta / reasoning.available são PENSAMENTO: não entram na resposta
            if "error" in d and not pedacos:
                return "erro: " + json.dumps(d.get("error"))[:300], time.time() - t0
        resp = "".join(pedacos).strip()
        if not resp and erro_hermes:
            return _erro_legivel(erro_hermes), time.time() - t0
        return resp or "(sem texto)", time.time() - t0


def porta_livre() -> int:
    """Porta livre escolhida na hora: evita colidir com backend órfão de outra
    execução (dois backends com tokens diferentes na mesma porta = confusão)."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return int(s.getsockname()[1])


def principal() -> int:
    global EM_TESTE
    ap = argparse.ArgumentParser()
    ap.add_argument("--teste", default="")
    ap.add_argument("--porta", type=int, default=0)
    args = ap.parse_args()
    EM_TESTE = bool(args.teste)   # --teste: stdout tem de ser UM json só (sem progresso)

    porta = args.porta or int(os.environ.get("PETAGENT_PORTA_BACKEND") or 0) or porta_livre()
    bk = Backend(porta)
    try:
        return asyncio.run(servico(bk, args))
    except KeyboardInterrupt:
        return 0


async def servico(bk: "Backend", args) -> int:
    """TUDO num único laço de eventos: o WebSocket vive neste laço — chamar
    asyncio.run duas vezes derrubava a conexão ('Event loop is closed')."""
    t0 = time.time()
    if not await bk.subir():
        log("não consegui subir o backend próprio")
        print(json.dumps({"ok": False, "pronto": False, "erro": "o backend do Hermes não iniciou"}), flush=True)
        return 3
    log(f"pronto em {time.time() - t0:.1f}s (porta {bk.porta})")

    if args.teste:
        resp, seg = await bk.perguntar(args.teste)
        print(json.dumps({"ok": True, "resposta": resp, "segundos": round(seg, 1)},
                         ensure_ascii=False), flush=True)
        _derruba_arvore(bk.proc)
        return 0

    # handshake: o app só pergunta depois disto
    # o modelo vai junto: o app mostra no cabeçalho do chat qual IA está respondendo
    print(json.dumps({"ok": True, "pronto": True, "sessao": bk.sessao, "modelo": bk.modelo,
                      "conversa": bk.chave}), flush=True)
    global _pedido_atual
    fila: asyncio.Queue = asyncio.Queue()
    leitor = asyncio.create_task(_le_stdin(bk, fila))
    while True:
        pedido = await fila.get()
        if pedido is None:
            break
        # o id volta em toda linha desta pergunta: se o app já desistiu dela (prazo), a
        # resposta atrasada não pode ser lida como resposta da pergunta seguinte
        _pedido_atual = pedido.get("id")
        if pedido.get("op"):
            try:
                dados = await bk.operacao(pedido)
            except Exception as e:  # noqa: BLE001
                log(f"op {pedido.get('op')} falhou: {type(e).__name__}: {e}")
                dados = {"erro": f"{type(e).__name__}: {e}"}
            print(json.dumps({"ok": "erro" not in dados, "id": _pedido_atual, "dados": dados,
                              "erro": dados.get("erro", "")}, ensure_ascii=False), flush=True)
            continue
        p = str(pedido.get("prompt") or "").strip()
        anexos = pedido.get("anexos") if isinstance(pedido.get("anexos"), list) else []
        if not p and not anexos:
            continue
        try:
            if pedido.get("conversa") and not await bk.usar_conversa(str(pedido["conversa"])):
                print(json.dumps({"ok": True, "id": _pedido_atual, "conversa": bk.chave,
                                  "resposta": "Não foi possível abrir esta conversa. Veja os detalhes no ponte.log."},
                                 ensure_ascii=False), flush=True)
                continue
            resp, seg = await bk.perguntar(p, anexos=anexos)
            print(json.dumps({"ok": True, "id": _pedido_atual, "resposta": resp,
                              "segundos": round(seg, 1), "modelo": bk.modelo,
                              "conversa": bk.chave}, ensure_ascii=False), flush=True)
        except Exception as e:  # noqa: BLE001
            print(json.dumps({"ok": False, "id": _pedido_atual,
                              "erro": f"{type(e).__name__}: {e}"}), flush=True)
    # o app fechou: leva o backend junto (sem órfãos — a árvore inteira, ver _derruba_arvore)
    leitor.cancel()
    _derruba_arvore(bk.proc)
    return 0


async def _le_stdin(bk: "Backend", fila: asyncio.Queue) -> None:
    """O ÚNICO leitor do stdin. A escolha de uma aprovação vai direto ao Hermes — ela chega
    enquanto o laço de `servico` está parado esperando a pergunta, que só termina DEPOIS dela.
    O resto (perguntas e operações) entra na fila, na ordem. None na fila = o app fechou."""
    try:
        while True:
            linha = await asyncio.to_thread(sys.stdin.readline)
            if not linha:
                break
            try:
                pedido = json.loads(linha)
            except json.JSONDecodeError:
                continue
            if not isinstance(pedido, dict):
                continue
            if pedido.get("aprovacao"):
                await bk.responde_aprovacao(str(pedido["aprovacao"]), str(pedido.get("escolha") or ""))
                continue
            await fila.put(pedido)
    finally:
        await fila.put(None)


# ---------------------------------------------------------------------------
# Helpers dos comandos de barra (10/10/2026)
# ---------------------------------------------------------------------------

def _erro_legivel(msg: str) -> str:
    """O que o chat mostra quando o Hermes falha ANTES de responder (22/10/2026).

    O Hermes avisa por um EVENTO `error` (payload.message) — e a ponte só olhava o campo
    `error` do topo, então o chat dizia "(sem texto)" em 0,1 s e ninguém sabia por quê. Caso
    medido: Anthropic escolhida e o Hermes sem o extra `anthropic` ("The 'anthropic' package
    is required for the Anthropic provider: ..."). O app instala esse extra ao salvar a IA e
    ao subir a ponte (inteligencia.go, garanteComplemento); se mesmo assim faltar, a pessoa lê
    o que fazer.
    """
    m = re.search(r"The '([\w.-]+)' package is required", msg)
    if m:
        return (f"Não consegui responder: o Hermes desta máquina não tem o complemento "
                f"`{m.group(1)}`, que esta IA exige, e a instalação automática falhou.\n\n"
                "Abra **Sistema › Inteligência** e clique em **Salvar**: eu tento instalar de novo e "
                "reinicio o agente. Se continuar, rode no terminal:\n"
                f"`hermes pm install --extra {m.group(1)}`")
    msg = re.sub(r"^Hermes could not start the assistant for this session\.\s*(Details:\s*)?", "", msg.strip())
    if len(msg) > 400:
        msg = msg[:400] + "…"
    return f"Não consegui responder, pois o Hermes informou um erro:\n\n{msg}\n\n(Detalhes no ponte.log.)"


def _sem_contexto_anexado(t: str) -> str:
    """A mensagem como a pessoa escreveu (22/10/2026). O Hermes guarda a mensagem com o
    conteúdo do `@file:` EMBUTIDO ("--- Attached Context ---" + o texto inteiro do arquivo) —
    medido no teste: reabrir a conversa mostrava o PDF todo dentro da bolha. A marca `@file:`
    fica (a tela a troca pelo chip do anexo); o conteúdo, não."""
    return t.split("\n--- Attached Context ---", 1)[0].rstrip()


def _dados_do_rpc(r) -> dict:
    """Extrai o dicionario util de uma resposta JSON-RPC do gateway.

    O gateway responde em {result: {...}} ou ja no formato plano, e alguns metodos
    aninham em {result: {data: {...}}}. Tentar as tres formas evita "nao consegui medir".
    """
    if not isinstance(r, dict):
        return {}
    for caminho in (("result", "data"), ("result",), ("data",), ()):
        d = r
        for chave in caminho:
            d = d.get(chave) if isinstance(d, dict) else None
        if isinstance(d, dict) and d:
            return d
    return {}


def _texto_do_rpc(r) -> str:
    """Extrai TEXTO de uma resposta de `slash.exec` (o formato varia por comando)."""
    d = _dados_do_rpc(r)
    if not d:
        return ""
    for chave in ("output", "text", "message", "result_text", "lines", "content"):
        v = d.get(chave)
        if isinstance(v, str) and v.strip():
            return v.strip()
        if isinstance(v, list):
            juntas = "\n".join(str(x) for x in v if x is not None)
            if juntas.strip():
                return juntas.strip()
    # ultimo recurso: o proprio dicionario serializado, para o dono ver algo util
    return ""


def _troca_reasoning(texto: str) -> tuple:
    """Troca `agent.reasoning_effort` para `low` no config do BONECO (10/10/2026).

    Medido: com `medium` (herdado do dono), uma pergunta simples levou 224 s — o log mostrou
    3.802 eventos de pensamento. O boneco é CHAT: `low` responde muito mais rápido.

    ⚠️ Troca APENAS a linha dentro do bloco `agent:` — o config do dono também tem
    `reasoning_effort` sob `agent:` e um replace global atingiria a seção errada.
    Devolve (texto, trocou).
    """
    linhas = texto.split("\n")
    dentro = False
    for i, l in enumerate(linhas):
        if l.startswith("agent:"):
            dentro = True
            continue
        if dentro and l and not l[0].isspace() and not l.startswith("#"):
            dentro = False                      # saiu do bloco agent:
        if dentro and l.strip().startswith("reasoning_effort:"):
            if "medium" in l:
                linhas[i] = l.replace("medium", "low")
                return "\n".join(linhas), True
            return "\n".join(linhas), False     # já está em outro valor: não mexo
    return texto, False


def _limita_iteracoes(texto: str) -> tuple:
    """Teto de MAX_ITERACOES_IA em `agent.max_turns` e `delegation.max_iterations` no
    config do BONECO (13/10/2026).

    Mesmo molde de _troca_reasoning: textual e só DENTRO do bloco certo — `goals:` também
    tem `max_turns` e não é para mexer. Valor de 1 a MAX_ITERACOES_IA fica (menos é mais
    barato); qualquer outro vira o teto — inclusive 0, -1 e "none", que no Hermes são
    ILIMITADO. Chave ausente entra no topo do bloco; bloco ausente vai no fim.
    Devolve (texto, mudou).
    """
    linhas = texto.split("\n")
    mudou = False
    for bloco, chave in (("agent", "max_turns"), ("delegation", "max_iterations")):
        inicio = next((i for i, l in enumerate(linhas) if l.startswith(bloco + ":")), None)
        if inicio is None:
            while linhas and not linhas[-1].strip():
                linhas.pop()
            linhas += ["", f"{bloco}:", f"  {chave}: {MAX_ITERACOES_IA}", ""]
            mudou = True
            continue
        recuo, achou = None, False
        for i in range(inicio + 1, len(linhas)):
            l = linhas[i]
            if l and not l[0].isspace() and not l.startswith("#"):
                break                               # saiu do bloco
            if not l.strip() or l.strip().startswith("#"):
                continue
            if recuo is None:
                recuo = l[:len(l) - len(l.lstrip())]
            if l.strip().startswith(chave + ":"):
                achou = True
                bruto = l.split(":", 1)[1].split("#")[0].strip().strip("\"'")
                try:
                    dentro = 1 <= int(bruto) <= MAX_ITERACOES_IA
                except ValueError:
                    dentro = False
                if not dentro:
                    linhas[i] = (f"{l[:len(l) - len(l.lstrip())]}{chave}: {MAX_ITERACOES_IA}"
                                 f"  # PetAgent: teto de creditos (era {bruto or 'vazio'})")
                    mudou = True
                break
        if not achou:
            linhas.insert(inicio + 1, f"{recuo or '  '}{chave}: {MAX_ITERACOES_IA}")
            mudou = True
    return "\n".join(linhas), mudou


def _paresDoCatalogo(r) -> list:
    """Extrai pares (comando, descrição) do `commands.catalog` do Hermes.

    O formato do gateway varia (categorias aninhadas, pares, dicts). Em vez de adivinhar a
    forma exata, varre a resposta inteira atrás de listas de dois textos — é o que o catálogo
    devolve para cada comando — e mantém a ordem em que aparecem.
    """
    achados: list = []
    vistos: set = set()

    def _anda(no):
        if len(achados) >= 80:
            return
        if isinstance(no, dict):
            for v in no.values():
                _anda(v)
        elif isinstance(no, list):
            if (len(no) == 2 and isinstance(no[0], str) and isinstance(no[1], str)
                    and no[0].strip() and no[0].strip() not in vistos):
                vistos.add(no[0].strip())
                achados.append((no[0].strip(), no[1].strip()))
            else:
                for v in no:
                    _anda(v)

    _anda(_dados_do_rpc(r))
    return achados


def _barraDeContexto(tokens: int) -> str:
    """Barra visual do tamanho do contexto — o dono pediu 'tamanho do contexto' no /status.

    A janela de referencia e a do modelo em uso; sem saber o limite exato, 128k e o piso
    com que o Hermes trabalha para os modelos atuais. O numero exato vem sempre acima.
    """
    limite = 128_000
    pct = min(100.0, tokens * 100.0 / limite)
    cheios = int(pct / 5)
    barra = "#" * cheios + "." * (20 - cheios)
    return f"[{barra}] ~{pct:.0f}% de {limite // 1000}k de contexto"


if __name__ == "__main__":
    try:
        raise SystemExit(principal())
    except KeyboardInterrupt:
        raise SystemExit(0)
