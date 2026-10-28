/* PetAgent — Tarefas do dia (27/10/2026). Pedido da dona: "estou numa reunião, alguém me
   pede uma coisa e eu não consigo fazer na hora: preciso mandar para o boneco, ele monta a
   minha lista do dia e me lembra antes". Pranchas aprovadas em 27/10/2026 (01 a 10).

   - Captura rápida (Ctrl+Alt+T de qualquer lugar, ou o campo do painel): texto ou áudio.
     A tarefa é salva NA HORA; depois o cartão pergunta o prazo (nada é obrigatório).
   - Painel Tarefas (menu Recursos ou o selo no boneco): Hoje · Semana · Mês · Feitas,
     edição (prazo, lembretes, repetição) e as configurações das cadências.
   - Lembretes: quem conta o tempo é o Go (tarefas_avisos.go). Aqui só chega o evento
     "tarefa-lembrete", que vira o balão com Feito / Adiar.
   - No chat: "tenho que…" sugere "Anotar como tarefa?"; "/tarefa …" anota direto (Go).
   Visual dos painéis Aparência/Inteligência (.aparencia / .ap-* / .ia-*). */

const TF_LEMBRETES = [[0, "Na hora"], [5, "5 min"], [15, "15 min"], [30, "30 min"], [60, "1 h"], [120, "2 h"], [1440, "1 dia"]];
const TF_REPETE = [["", "Não repete"], ["diaria", "Todo dia"], ["uteis", "Dias úteis"], ["semanal", "Toda semana"], ["mensal", "Todo mês"]];
const TF_INSISTIR = [[15, "15 min"], [30, "30 min"], [60, "1 h"], [0, "Não insistir"]];
const TF_DIAS = ["D", "S", "T", "Q", "Q", "S", "S"];
const TF_DIA_NOME = ["Domingo", "Segunda", "Terça", "Quarta", "Quinta", "Sexta", "Sábado"];
const TF_DIA_CURTO = ["dom", "seg", "ter", "qua", "qui", "sex", "sáb"];
const TF_MESES = ["janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"];
const TF_PERGUNTA_MS = 20000;  // o cartão de perguntas some sozinho (a tarefa já está salva)
const TF_SUGESTAO_MS = 25000;
// "tenho que…", "preciso…", "lembra de…", "não esquecer…" — verificação local, sem IA
const TF_SUGERE = /(^|[^\p{L}])(tenho que|tem que|temos que|precis[oa]|precisamos|lembr(a|ar|e) de|n[ãa]o (posso )?esquec)/iu;

const tf = {
  tarefas: [], cfg: null, atalhos: [], atalhoErro: "",
  vista: "lista",        // lista | editar | config
  aba: "hoje",           // hoje | semana | mes | feitas
  semana: 0,             // semanas a partir da atual
  mes: null,             // { y, m } do calendário
  diaSel: "",
  ed: null, confirmaExcluir: false,
  cf: null,
  status: null,          // { tipo: ok | erro | info, texto }
  fila: [], mostrando: false,
};
let painelTF = null, capTF = null;
const cap = { etapa: "escrever", texto: "", erro: "", tarefa: null, data: "", hora: "", escolhendo: false, fim: 0, vigia: null };

/* ───────── utilidades ───────── */

function escTF(t) {
  return String(t ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}
function dstr(d) { return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0"); }
function ddia(s) { const [y, m, d] = String(s).split("-").map(Number); return new Date(y, m - 1, d); }
function somaDias(s, n) { const d = ddia(s); d.setDate(d.getDate() + n); return dstr(d); }
const hojeTF = () => dstr(new Date());
const agoraHM = () => new Date().toTimeString().slice(0, 5);
const ddmm = (s) => { const d = ddia(s); return String(d.getDate()).padStart(2, "0") + "/" + String(d.getMonth() + 1).padStart(2, "0"); };

function quandoTF(t) {
  if (!t.data) return "sem data";
  const h = hojeTF();
  let dia;
  if (t.data === h) dia = "hoje";
  else if (t.data === somaDias(h, 1)) dia = "amanhã";
  else if (t.data === somaDias(h, -1)) dia = "ontem";
  else dia = TF_DIA_CURTO[ddia(t.data).getDay()] + " " + ddmm(t.data);
  return t.hora ? dia + " às " + t.hora : dia;
}
function duracaoTF(m) {
  if (m < 60) return m + " min";
  if (m < 1440) return Math.floor(m / 60) + " h" + (m % 60 ? " " + String(m % 60).padStart(2, "0") : "");
  const d = Math.floor(m / 1440);
  return d === 1 ? "1 dia" : d + " dias";
}
const juntaE = (xs) => xs.length < 2 ? xs.join("") : xs.slice(0, -1).join(", ") + " e " + xs[xs.length - 1];
const lembretesDe = (t) => t.lembretes == null ? ((tf.cfg && tf.cfg.lembretes) || [0, 15, 60]) : t.lembretes;
function textoLembretes(t) {
  if (!t.data) return "Sem data, sem lembrete";
  const ls = lembretesDe(t);
  if (!ls.length) return "Sem lembrete";
  if (!t.hora) return "Lembrar às " + ((tf.cfg && tf.cfg.hora_sem_horario) || "09:00") + " do dia";
  return "Lembrar " + juntaE(ls.slice().sort((a, b) => b - a).map((m) => m === 0 ? "na hora" : duracaoTF(m) + " antes"));
}
function textoRepete(r) {
  if (!r || !r.tipo) return "";
  if (r.tipo === "diaria") return "todo dia";
  if (r.tipo === "uteis") return "dias úteis";
  if (r.tipo === "mensal") return "todo dia " + (r.dia_mes || "");
  const dias = r.dias || [];
  if (dias.length === 1) return (dias[0] === 0 || dias[0] === 6 ? "todo " : "toda ") + TF_DIA_NOME[dias[0]].toLowerCase();
  return "toda semana (" + dias.map((d) => TF_DIA_CURTO[d]).join(", ") + ")";
}
const maiuscula = (s) => s ? s[0].toUpperCase() + s.slice(1) : s;
// gravando ou transcrevendo um áudio do painel/captura: não repintar (o campo sumiria)
const vozOcupadaTF = () => typeof gravandoVoz === "function" && (gravandoVoz() || voz.transcrevendo) && !!voz.aoTexto;
const repintaListaTF = () => { if (tfAberto() && tf.vista === "lista" && !vozOcupadaTF()) pintaTF(); };
function nomeAtalho() {
  const id = (tf.cfg && tf.cfg.atalho) || "ctrl+alt+t";
  const a = tf.atalhos.find((x) => x.id === id);
  return a ? a.nome.replace(/ \+ /g, "+") : "Ctrl+Alt+T";
}
function ordemTF(a, b) {
  if (!!a.feita !== !!b.feita) return a.feita ? 1 : -1;
  return (a.data || "9999").localeCompare(b.data || "9999") || (a.hora || "99").localeCompare(b.hora || "99") || String(a.criada).localeCompare(String(b.criada));
}
// passou da entrega: data anterior a hoje, ou hoje com horário que já passou
const vencida = (t) => !t.feita && !!t.data && (t.data < hojeTF() || (t.data === hojeTF() && !!t.hora && t.hora < agoraHM()));

function contagemTF() {
  const h = hojeTF();
  let pend = 0, feitas = 0, atras = 0, venc = 0;
  for (const t of tf.tarefas) {
    if (t.feita) { if (String(t.feita_em || "").startsWith(h)) feitas++; continue; }
    if (t.data === h) pend++;
    else if (t.data && t.data < h) atras++;
    if (vencida(t)) venc++;
  }
  return { pend, feitas, atras, venc };
}

function descTarefas() {
  const c = contagemTF(), n = c.pend + c.atras;
  return (n ? n + (n === 1 ? " pendente" : " pendentes") + (c.atras ? " (" + c.atras + " atrasada" + (c.atras > 1 ? "s" : "") + ")" : " hoje") : "Nada pendente hoje") + " · " + nomeAtalho() + " anota";
}

async function carregaTF(comCfg) {
  if (typeof G === "undefined" || !G || !G.TarefasListar) return;
  try { tf.tarefas = JSON.parse(await G.TarefasListar()).tarefas || []; } catch (e) { }
  if (comCfg || !tf.cfg) {
    try {
      const c = JSON.parse(await G.TarefasConfig());
      tf.cfg = c.config; tf.atalhos = c.atalhos || []; tf.atalhoErro = c.atalho_erro || "";
    } catch (e) { }
  }
  pintaSeloTF();
}

const reposiciona = (el) => { if (typeof posicionaPertoDoPet === "function") posicionaPertoDoPet(el); if (typeof reportarHit === "function") reportarHit(); };
const respTF = async (p) => { try { return JSON.parse(await p); } catch (e) { return { ok: false, erro: String(e) }; } };

/* ───────── selo no boneco (prancha 09) ───────── */

function pintaSeloTF() {
  const petEl = document.getElementById("pet");
  if (!petEl) return;
  let s = document.getElementById("tf-selo");
  if (!s) {
    s = document.createElement("button");
    s.id = "tf-selo";
    s.type = "button";
    s.className = "tf-selo oculto";
    // o selo é do painel, não do boneco: nada de arrastar, carinho ou chat
    ["mousedown", "dblclick", "contextmenu"].forEach((ev) => s.addEventListener(ev, (e) => e.stopPropagation()));
    s.addEventListener("click", (e) => { e.stopPropagation(); abrirTarefas(); });
    petEl.appendChild(s);
  }
  const c = contagemTF(), n = c.pend + c.atras;
  s.textContent = n > 9 ? "9+" : String(n);
  s.classList.toggle("oculto", n === 0);
  s.classList.toggle("atraso", c.venc > 0);
  const rot = n + (n === 1 ? " tarefa pendente" : " tarefas pendentes") + (c.venc ? ", " + c.venc + " com o prazo vencido" : "") + ". Clique para abrir as tarefas.";
  s.title = rot;
  s.setAttribute("aria-label", rot);
}

/* ───────── painel ───────── */

const tfAberto = () => !!painelTF && !painelTF.classList.contains("oculto");

async function abrirTarefas(vista, id) {
  if (!painelTF) {
    painelTF = document.createElement("div");
    painelTF.className = "aparencia tf oculto";
    painelTF.setAttribute("role", "dialog");
    painelTF.setAttribute("aria-label", "Tarefas");
    document.body.appendChild(painelTF);
  }
  if (typeof fecharMenu === "function") fecharMenu();
  await carregaTF(true);
  tf.vista = "lista"; tf.status = null;
  if (vista === "editar" && id) abreEdicao(id);
  else if (vista === "config") abreConfig();
  painelTF.classList.remove("oculto");
  pintaTF();
}

function fecharTarefas() {
  if (!tfAberto()) return;
  painelTF.classList.add("oculto");
  if (typeof reportarHit === "function") reportarHit();
}

function pintaTF() {
  if (!painelTF) return;
  if (tf.vista === "editar" && tf.ed) painelTF.innerHTML = htmlEdicao();
  else if (tf.vista === "config" && tf.cf) painelTF.innerHTML = htmlConfig();
  else { tf.vista = "lista"; painelTF.innerHTML = htmlLista(); }
  ligaTF();
  if (typeof pintaBotoesVoz === "function") pintaBotoesVoz();
  reposiciona(painelTF);
}

const statusTF = () => tf.status ? `<div class="ia-status ${tf.status.tipo}" role="status">${escTF(tf.status.texto)}</div>` : "";

function dataLonga(s) {
  const d = ddia(s);
  return TF_DIA_NOME[d.getDay()] + ", " + d.getDate() + " de " + TF_MESES[d.getMonth()];
}

function itemTF(t, modo) {
  const meta = [];
  if (modo === "hoje") { if (t.hora) meta.push(t.hora); }
  else if (modo === "dia") { if (t.hora) meta.push(t.hora); }
  else if (modo === "feitas") meta.push("feita " + quandoTF({ data: String(t.feita_em || "").slice(0, 10), hora: String(t.feita_em || "").slice(11, 16) }));
  else meta.push(quandoTF(t));
  if (vencida(t) && modo !== "atrasadas") meta.push("atrasada");
  const rep = textoRepete(t.repete);
  if (rep) meta.push(rep);
  if (t.feita && modo !== "feitas" && t.feita_em) meta.push("feita às " + String(t.feita_em).slice(11, 16));
  if (t.origem === "audio" && !t.feita) meta.push("anotada por áudio");
  if (t.nota) meta.push(t.nota);
  const comLembrete = !t.feita && t.data && lembretesDe(t).length > 0;
  return `<div class="tf-item${t.feita ? " feita" : ""}${vencida(t) ? " venc" : ""}">
    <input type="checkbox" data-check="${escTF(t.id)}" ${t.feita ? "checked" : ""} aria-label="${t.feita ? "Desmarcar" : "Marcar como feita"}: ${escTF(t.titulo)}">
    <button class="tf-item-txt" data-editar="${escTF(t.id)}" title="Editar">
      <span class="tf-tit">${escTF(t.titulo)}</span>${meta.length ? `<span class="tf-meta">${escTF(meta.join(" · "))}</span>` : ""}
    </button>
    ${rep ? `<span class="tf-ic" title="Repete: ${escTF(rep)}">${icone("repetir", 15)}</span>` : ""}
    ${comLembrete ? `<span class="tf-ic acento" title="${escTF(textoLembretes(t))}">${icone("sino", 15)}</span>` : ""}
  </div>`;
}

const secaoTF = (nome, lista, modo, cls) => lista.length
  ? `<div class="tf-sec ${cls || ""}">${escTF(nome)}</div>` + lista.map((t) => itemTF(t, modo)).join("") : "";

function htmlHoje() {
  const h = hojeTF(), c = contagemTF();
  const atrasadas = tf.tarefas.filter((t) => !t.feita && t.data && t.data < h).sort(ordemTF);
  const doDia = tf.tarefas.filter((t) => t.data === h).sort(ordemTF);
  const semData = tf.tarefas.filter((t) => !t.feita && !t.data).sort(ordemTF);
  const numeros = `<div class="pn-numeros tf-num">
    <div class="pn-num acento"><b>${c.pend}</b><span>Pendentes</span></div>
    <div class="pn-num ok"><b>${c.feitas}</b><span>Feitas hoje</span></div>
    <div class="pn-num${c.atras ? " erro" : ""}"><b>${c.atras}</b><span>${c.atras === 1 ? "Atrasada" : "Atrasadas"}</span></div></div>`;
  const corpo = secaoTF("Atrasadas", atrasadas, "atrasadas", "erro") + secaoTF("Hoje", doDia, "hoje") + secaoTF("Sem data", semData, "semdata");
  return numeros + `<div class="tf-lista">${corpo || `<p class="ia-nota tf-vazio">Nenhuma tarefa para hoje. Anote a primeira no campo abaixo ou com ${escTF(nomeAtalho())}, de qualquer programa.</p>`}</div>`;
}

function htmlSemana() {
  const h = hojeTF();
  const seg = somaDias(h, -((ddia(h).getDay() + 6) % 7) + 7 * tf.semana);
  const dom = somaDias(seg, 6);
  let dias = "";
  for (let i = 0; i < 7; i++) {
    const d = somaDias(seg, i);
    const ts = tf.tarefas.filter((t) => t.data === d).sort(ordemTF);
    const pend = ts.filter((t) => !t.feita).length, feitas = ts.length - pend;
    const resumo = !ts.length ? "livre" : [pend ? pend + (pend === 1 ? " pendente" : " pendentes") : "", feitas ? feitas + (feitas === 1 ? " feita" : " feitas") : ""].filter(Boolean).join(", ");
    dias += `<div class="tf-semdia${d === h ? " hoje" : ""}">
      <div class="tf-semdia-topo"><b>${TF_DIA_CURTO[ddia(d).getDay()]} ${ddmm(d)}</b><span>${d === h ? "hoje · " : ""}${resumo}</span></div>
      ${ts.map((t) => itemTF(t, "dia")).join("")}</div>`;
  }
  return `<div class="tf-nav"><button class="botao-x" data-sem="-1" aria-label="Semana anterior">${icone("voltar", 16)}</button>
      <b>${ddmm(seg)} a ${ddmm(dom)}</b>
      <button class="botao-x" data-sem="1" aria-label="Próxima semana">${icone("seta", 16)}</button>
      ${tf.semana ? `<button class="ia-link tf-link" data-sem="0">Esta semana</button>` : ""}</div>
    <div class="tf-lista">${dias}</div>`;
}

function htmlMes() {
  const h = hojeTF();
  if (!tf.mes) { const d = ddia(h); tf.mes = { y: d.getFullYear(), m: d.getMonth() }; }
  const { y, m } = tf.mes;
  const prim = new Date(y, m, 1), ult = new Date(y, m + 1, 0).getDate();
  const doMes = tf.tarefas.filter((t) => t.data && t.data.startsWith(dstr(prim).slice(0, 7)));
  if (!tf.diaSel || !tf.diaSel.startsWith(dstr(prim).slice(0, 7))) tf.diaSel = h.startsWith(dstr(prim).slice(0, 7)) ? h : dstr(prim);
  let cels = "";
  for (let i = 0; i < prim.getDay(); i++) cels += `<span class="tf-cel vazia"></span>`;
  for (let n = 1; n <= ult; n++) {
    const d = dstr(new Date(y, m, n));
    const ts = doMes.filter((t) => t.data === d);
    const pend = ts.filter((t) => !t.feita);
    const tom = !ts.length ? "" : pend.some(vencida) ? "atraso" : pend.length ? "pend" : "feitas";
    const qtd = ts.length ? `<i class="tf-qtd ${tom}">${pend.length || ts.length}</i>` : "";
    const rot = n + " de " + TF_MESES[m] + (ts.length ? ": " + ts.length + (ts.length === 1 ? " tarefa" : " tarefas") + (pend.length ? ", " + pend.length + " pendente" + (pend.length > 1 ? "s" : "") : ", todas feitas") : "");
    cels += `<button class="tf-cel${d === h ? " hoje" : ""}${d === tf.diaSel ? " sel" : ""}${d < h ? " passado" : ""}" data-dia="${d}" aria-label="${escTF(rot)}"><span>${n}</span>${qtd}</button>`;
  }
  const doDia = tf.tarefas.filter((t) => t.data === tf.diaSel).sort(ordemTF);
  const feitas = doMes.filter((t) => t.feita).length;
  return `<div class="tf-nav"><button class="botao-x" data-mes="-1" aria-label="Mês anterior">${icone("voltar", 16)}</button>
      <b>${TF_MESES[m][0].toUpperCase() + TF_MESES[m].slice(1)} de ${y}</b>
      <button class="botao-x" data-mes="1" aria-label="Próximo mês">${icone("seta", 16)}</button>
      <span class="tf-nav-sub">${doMes.length} ${doMes.length === 1 ? "tarefa" : "tarefas"} · ${feitas} ${feitas === 1 ? "feita" : "feitas"}</span></div>
    <div class="tf-grade">${TF_DIA_CURTO.map((d) => `<span class="tf-sem">${d}</span>`).join("")}${cels}</div>
    <div class="tf-legenda"><span><i class="pend"></i>pendentes</span><span><i class="feitas"></i>todas feitas</span><span><i class="atraso"></i>com atraso</span></div>
    <div class="tf-sec">${escTF(dataLonga(tf.diaSel))}${tf.diaSel === h ? " · hoje" : ""}</div>
    <div class="tf-lista">${doDia.map((t) => itemTF(t, "dia")).join("") || `<p class="ia-nota tf-vazio">Nenhuma tarefa neste dia.</p>`}</div>`;
}

function htmlFeitas() {
  const ts = tf.tarefas.filter((t) => t.feita).sort((a, b) => String(b.feita_em).localeCompare(String(a.feita_em))).slice(0, 60);
  return `<div class="tf-lista">${ts.map((t) => itemTF(t, "feitas")).join("") || `<p class="ia-nota tf-vazio">Nenhuma tarefa concluída ainda.</p>`}</div>
    ${ts.length ? `<p class="ia-nota">Desmarque uma tarefa para ela voltar à lista.</p>` : ""}`;
}

function htmlLista() {
  const corpo = { hoje: htmlHoje, semana: htmlSemana, mes: htmlMes, feitas: htmlFeitas }[tf.aba] || htmlHoje;
  return `
    <div class="ap-topo">${icone("tarefas")}<b>Tarefas</b>
      <button class="botao-x" data-cfg aria-label="Configurações de lembretes" title="Configurações de lembretes">${icone("ajustes", 18)}</button>
      <button class="botao-x" data-fechar aria-label="Fechar">${icone("fechar", 18)}</button></div>
    <p class="ap-dica">${escTF(dataLonga(hojeTF()))}</p>
    <div class="ap-seg tf-abas" role="tablist">${[["hoje", "Hoje"], ["semana", "Semana"], ["mes", "Mês"], ["feitas", "Feitas"]].map(([k, r]) =>
      `<button role="tab" data-aba="${k}" class="${tf.aba === k ? "sel" : ""}" aria-selected="${tf.aba === k}">${r}</button>`).join("")}</div>
    ${corpo()}
    ${statusTF()}
    <div class="tf-nova">
      <div class="campo-msg"><textarea id="tf-nova" rows="1" placeholder="+ Nova tarefa (Enter para salvar)" aria-label="Nova tarefa" autocomplete="off"></textarea></div>
      <button id="tf-nova-voz" class="voz-mic" aria-label="Gravar áudio"></button>
    </div>`;
}

/* ───────── edição (prancha 07) ───────── */

function abreEdicao(id) {
  const t = tf.tarefas.find((x) => x.id === id);
  if (!t) { tf.status = { tipo: "erro", texto: "Essa tarefa não existe mais." }; return; }
  tf.vista = "editar";
  tf.ed = JSON.parse(JSON.stringify(t));
  tf.ed.repete = tf.ed.repete || { tipo: "" };
  tf.confirmaExcluir = false;
  tf.status = null;
}

const chipTF = (on, attrs, rot) => `<button class="tf-chip${on ? " sel" : ""}" aria-pressed="${on}" ${attrs}>${escTF(rot)}</button>`;

function htmlEdicao() {
  const e = tf.ed, ls = lembretesDe(e), r = e.repete || { tipo: "" };
  const insistir = (tf.cfg && tf.cfg.insistir_min) || 0;
  return `
    <div class="ap-topo"><button class="botao-x" data-voltar aria-label="Voltar para a lista">${icone("voltar", 18)}</button><b>Editar tarefa</b>
      <button class="botao-x" data-fechar aria-label="Fechar">${icone("fechar", 18)}</button></div>
    <div class="ap-sec"><label class="tf-rot" for="tf-e-tit">Tarefa</label>
      <input class="ia-campo" id="tf-e-tit" data-campo="titulo" value="${escTF(e.titulo)}" autocomplete="off"></div>
    <div class="ap-sec"><label class="tf-rot" for="tf-e-nota">Anotação (opcional)</label>
      <input class="ia-campo" id="tf-e-nota" data-campo="nota" value="${escTF(e.nota || "")}" autocomplete="off"></div>
    <div class="tf-2">
      <div class="ap-sec"><label class="tf-rot" for="tf-e-data">Data de entrega</label>
        <input class="ia-campo" id="tf-e-data" type="date" data-campo="data" value="${escTF(e.data || "")}"></div>
      <div class="ap-sec"><label class="tf-rot" for="tf-e-hora">Horário</label>
        <input class="ia-campo" id="tf-e-hora" type="time" data-campo="hora" value="${escTF(e.hora || "")}" ${e.data ? "" : "disabled"}></div>
    </div>
    <div class="ap-sec"><span>Lembrar antes</span>
      <div class="tf-chips">${TF_LEMBRETES.map(([m, rot]) => chipTF(ls.includes(m), `data-lembrete="${m}"`, rot)).join("")}</div>
      <p class="ia-nota">${!e.data ? "Escolha uma data para receber lembretes." : e.lembretes == null
        ? "Usando o padrão das configurações."
        : `Lembretes próprios desta tarefa. <button class="ia-link tf-link" data-padrao>Voltar ao padrão</button>`}${e.data && !e.hora ? " Sem horário, o lembrete sai às " + escTF((tf.cfg && tf.cfg.hora_sem_horario) || "09:00") + " do dia." : ""}</p></div>
    <div class="ap-sec"><span>Repetir</span>
      <div class="tf-chips">${TF_REPETE.map(([k, rot]) => chipTF((r.tipo || "") === k, `data-repete="${k}"`, rot)).join("")}</div>
      ${r.tipo === "semanal" ? `<div class="tf-dias"><span class="tf-rot">Nos dias</span>${TF_DIAS.map((d, i) =>
        `<button class="tf-diab${(r.dias || []).includes(i) ? " sel" : ""}" data-diasem="${i}" aria-pressed="${(r.dias || []).includes(i)}" aria-label="${TF_DIA_NOME[i]}">${d}</button>`).join("")}</div>` : ""}
      ${r.tipo === "mensal" ? `<div class="tf-linha"><label class="tf-rot" for="tf-e-diames">No dia</label>
        <input class="ia-campo tf-curto" id="tf-e-diames" type="number" min="1" max="31" data-diames value="${escTF(r.dia_mes || (e.data ? ddia(e.data).getDate() : ""))}"><span class="tf-rot">de cada mês</span></div>` : ""}
      ${r.tipo ? `<p class="ia-nota">Ao marcar como feita, a próxima ocorrência é criada sozinha.</p>` : ""}
    </div>
    <label class="tf-check"><input type="checkbox" data-insistir ${!e.sem_insistir && insistir ? "checked" : ""} ${insistir ? "" : "disabled"}>
      ${insistir ? "Se atrasar, insistir a cada " + escTF(duracaoTF(insistir)) : "A insistência está desligada nas configurações"}</label>
    ${statusTF()}
    <div class="ia-acoes tf-acoes">
      <button class="ia-bt sec tf-perigo" data-excluir>${icone("lixeira", 15)}${tf.confirmaExcluir ? "Confirmar exclusão" : "Excluir"}</button>
      <span class="tf-esp"></span>
      <button class="ia-bt sec" data-voltar>Cancelar</button>
      <button class="ia-bt" data-salvar>Salvar</button>
    </div>`;
}

async function salvaEdicao() {
  const e = tf.ed;
  if (!e.data) e.hora = "";
  const r = await respTF(G.TarefaAtualizar(JSON.stringify(e)));
  if (!r.ok) { tf.status = { tipo: "erro", texto: fraseUI(r.erro || "não foi possível salvar") }; pintaTF(); return; }
  await carregaTF();
  tf.vista = "lista"; tf.ed = null;
  tf.status = { tipo: "ok", texto: "Tarefa salva: " + r.tarefa.titulo + " · " + quandoTF(r.tarefa) + "." };
  pintaTF();
}

async function excluiEdicao() {
  if (!tf.confirmaExcluir) { tf.confirmaExcluir = true; pintaTF(); return; }
  const r = await respTF(G.TarefaExcluir(tf.ed.id));
  if (!r.ok) { tf.status = { tipo: "erro", texto: fraseUI(r.erro || "não foi possível excluir") }; pintaTF(); return; }
  await carregaTF();
  tf.vista = "lista"; tf.ed = null;
  tf.status = { tipo: "ok", texto: "Tarefa excluída." };
  pintaTF();
}

/* ───────── configurações (prancha 08) ───────── */

function abreConfig() {
  tf.vista = "config";
  tf.cf = JSON.parse(JSON.stringify(tf.cfg || {}));
  tf.status = tf.atalhoErro ? { tipo: "erro", texto: tf.atalhoErro } : null;
}

function htmlConfig() {
  const c = tf.cf, ls = c.lembretes || [];
  return `
    <div class="ap-topo"><button class="botao-x" data-voltar aria-label="Voltar para a lista">${icone("voltar", 18)}</button><b>Lembretes das tarefas</b>
      <button class="botao-x" data-fechar aria-label="Fechar">${icone("fechar", 18)}</button></div>
    <p class="ap-dica">Vale para as tarefas novas e para as que usam o padrão. Cada tarefa pode ter os seus.</p>
    <div class="ap-sec"><span>Antecedência padrão</span>
      <div class="tf-chips">${TF_LEMBRETES.map(([m, rot]) => chipTF(ls.includes(m), `data-cflembrete="${m}"`, rot)).join("")}</div></div>
    <div class="tf-linha"><label class="tf-rot tf-cresce" for="tf-c-semhora">Tarefa sem horário: lembrar às</label>
      <input class="ia-campo tf-curto" id="tf-c-semhora" type="time" data-cfcampo="hora_sem_horario" value="${escTF(c.hora_sem_horario)}"></div>
    <div class="ap-sec"><span>Se a tarefa atrasar, lembrar de novo a cada</span>
      <div class="tf-chips">${TF_INSISTIR.map(([m, rot]) => chipTF((c.insistir_min || 0) === m, `data-cfinsistir="${m}"`, rot)).join("")}</div></div>
    <div class="tf-linha"><label class="tf-rot tf-cresce" for="tf-c-ate">Parar de insistir às</label>
      <input class="ia-campo tf-curto" id="tf-c-ate" type="time" data-cfcampo="insistir_ate" value="${escTF(c.insistir_ate)}" ${c.insistir_min ? "" : "disabled"}></div>
    <label class="tf-check"><input type="checkbox" data-cfbool="pausa_fim_semana" ${c.pausa_fim_semana ? "checked" : ""}>Não insistir aos sábados e domingos</label>
    <div class="tf-sep"></div>
    <label class="tf-check"><input type="checkbox" data-cfbool="windows" ${c.windows ? "checked" : ""}>Notificação do Windows junto com o balão</label>
    <label class="tf-check"><input type="checkbox" data-cfbool="perguntar_prazo" ${c.perguntar_prazo ? "checked" : ""}>Perguntar o prazo ao anotar uma tarefa</label>
    <label class="tf-check"><input type="checkbox" data-cfbool="resumo_manha" ${c.resumo_manha ? "checked" : ""}>Resumo da manhã com as tarefas do dia</label>
    <div class="tf-linha"><label class="tf-rot tf-cresce" for="tf-c-atalho">Atalho para anotar</label>
      <select class="ia-campo tf-medio" id="tf-c-atalho" data-cfcampo="atalho">${tf.atalhos.map((a) =>
        `<option value="${escTF(a.id)}" ${a.id === c.atalho ? "selected" : ""}>${escTF(a.nome)}</option>`).join("")}</select></div>
    <p class="ia-nota">O atalho funciona em qualquer programa, inclusive durante uma reunião.</p>
    ${statusTF()}
    <div class="ia-acoes"><button class="ia-bt sec" data-voltar>Cancelar</button><button class="ia-bt" data-cfsalvar>Salvar</button></div>`;
}

async function salvaConfig() {
  const r = await respTF(G.TarefasConfigSalvar(JSON.stringify(tf.cf)));
  if (!r.ok) { tf.status = { tipo: "erro", texto: fraseUI(r.erro || "não foi possível salvar") }; pintaTF(); return; }
  tf.cfg = r.config; tf.atalhoErro = r.atalho_erro || "";
  if (tf.atalhoErro) { tf.cf = JSON.parse(JSON.stringify(tf.cfg)); tf.status = { tipo: "erro", texto: tf.atalhoErro }; pintaTF(); return; }
  tf.vista = "lista"; tf.cf = null;
  tf.status = { tipo: "ok", texto: "Configurações salvas." };
  await carregaTF();
  pintaTF();
}

/* ───────── ações da lista ───────── */

async function concluiTF(id, feita) {
  const r = await respTF(G.TarefaConcluir(id, feita));
  if (!r.ok) { tf.status = { tipo: "erro", texto: fraseUI(r.erro || "não foi possível marcar") }; }
  const antes = contagemTF();
  await carregaTF();
  const c = contagemTF();
  if (feita && r.ok && antes.pend + antes.atras > 0 && c.pend + c.atras === 0 && c.feitas > 0 && typeof tarefaConcluida === "function") {
    tarefaConcluida("Tudo feito por hoje!", "Todas as tarefas de hoje foram concluídas.");
  }
  repintaListaTF();
  return r;
}

// anotar de uma vez (campo do painel, áudio do painel): entende, salva e mostra onde caiu
async function anotaRapido(texto, origem) {
  texto = String(texto || "").trim();
  if (!texto) return;
  tf.status = { tipo: "info", texto: "Anotando…" }; pintaTF();
  const ent = await entendeTF(texto);
  const r = await respTF(G.TarefaCriar(JSON.stringify({ titulo: ent.titulo || texto, data: ent.data, hora: ent.hora, repete: ent.repete, origem, lembretes: null })));
  await carregaTF();
  tf.status = r.ok ? { tipo: "ok", texto: "Anotei: " + r.tarefa.titulo + " · " + quandoTF(r.tarefa) + "." } : { tipo: "erro", texto: fraseUI(r.erro || "não foi possível anotar") };
  pintaTF();
  const n = painelTF && painelTF.querySelector("#tf-nova");
  if (n) n.focus();
}

async function entendeTF(texto) {
  const r = await respTF(G.TarefaInterpretar(texto));
  return (r.ok && r.entendida) || { titulo: texto, data: "", hora: "", repete: { tipo: "" } };
}

function ligaMic(idBt, idTxt, aoTexto, raiz) {
  const b = raiz.querySelector("#" + idBt), input = raiz.querySelector("#" + idTxt);
  if (!b || !input || typeof vozComeca !== "function") { if (b) b.remove(); return; }
  if (typeof VOZ_BOTOES !== "undefined" && !VOZ_BOTOES.some(([x]) => x === idBt)) VOZ_BOTOES.push([idBt, idTxt]);
  b.onclick = () => { if (gravandoVoz()) vozPara(true); else vozComeca(b, input, aoTexto); };
}

function ligaTF() {
  const p = painelTF, q = (s) => p.querySelector(s), todos = (s, f) => p.querySelectorAll(s).forEach(f);
  todos("[data-fechar]", (b) => b.onclick = fecharTarefas);
  todos("[data-voltar]", (b) => b.onclick = () => { tf.vista = "lista"; tf.ed = null; tf.cf = null; tf.status = null; pintaTF(); });
  if (q("[data-cfg]")) q("[data-cfg]").onclick = () => { abreConfig(); pintaTF(); };
  todos("[data-aba]", (b) => b.onclick = () => { tf.aba = b.dataset.aba; tf.status = null; pintaTF(); });
  todos("[data-sem]", (b) => b.onclick = () => { const v = Number(b.dataset.sem); tf.semana = v ? tf.semana + v : 0; pintaTF(); });
  todos("[data-mes]", (b) => b.onclick = () => {
    const d = new Date(tf.mes.y, tf.mes.m + Number(b.dataset.mes), 1);
    tf.mes = { y: d.getFullYear(), m: d.getMonth() }; tf.diaSel = ""; pintaTF();
  });
  todos("[data-dia]", (b) => b.onclick = () => { tf.diaSel = b.dataset.dia; pintaTF(); });
  todos("[data-check]", (c) => c.onchange = () => concluiTF(c.dataset.check, c.checked));
  todos("[data-editar]", (b) => b.onclick = () => { abreEdicao(b.dataset.editar); pintaTF(); });

  const nova = q("#tf-nova");
  if (nova) {
    nova.onkeydown = (e) => {
      if (e.key !== "Enter" || e.shiftKey || e.isComposing) return;
      e.preventDefault();
      anotaRapido(nova.value, "texto");
    };
    ligaMic("tf-nova-voz", "tf-nova", (t) => anotaRapido(t, "audio"), p);
  }

  // edição
  if (tf.vista === "editar" && tf.ed) {
    const e = tf.ed;
    todos("[data-campo]", (el) => el.oninput = () => {
      e[el.dataset.campo] = el.value;
      if (el.dataset.campo === "data") { const h = q("#tf-e-hora"); if (h) h.disabled = !el.value; }
    });
    q("#tf-e-data").onchange = () => pintaMantendoFoco("#tf-e-data");
    todos("[data-lembrete]", (b) => b.onclick = () => {
      const m = Number(b.dataset.lembrete), ls = lembretesDe(e).slice();
      e.lembretes = ls.includes(m) ? ls.filter((x) => x !== m) : ls.concat(m).sort((a, z) => a - z);
      pintaTF();
    });
    if (q("[data-padrao]")) q("[data-padrao]").onclick = () => { e.lembretes = null; pintaTF(); };
    todos("[data-repete]", (b) => b.onclick = () => {
      e.repete = { tipo: b.dataset.repete };
      if (e.repete.tipo === "semanal") e.repete.dias = [ddia(e.data || hojeTF()).getDay()];
      if (e.repete.tipo === "mensal") e.repete.dia_mes = ddia(e.data || hojeTF()).getDate();
      pintaTF();
    });
    todos("[data-diasem]", (b) => b.onclick = () => {
      const d = Number(b.dataset.diasem), ds = e.repete.dias || [];
      const novo = ds.includes(d) ? ds.filter((x) => x !== d) : ds.concat(d).sort();
      if (novo.length) e.repete.dias = novo; // pelo menos um dia
      pintaTF();
    });
    if (q("[data-diames]")) q("[data-diames]").oninput = (ev) => { e.repete.dia_mes = Number(ev.target.value) || 0; };
    if (q("[data-insistir]")) q("[data-insistir]").onchange = (ev) => { e.sem_insistir = !ev.target.checked; };
    q("[data-salvar]").onclick = salvaEdicao;
    q("[data-excluir]").onclick = excluiEdicao;
    q("#tf-e-tit").onkeydown = (ev) => { if (ev.key === "Enter") salvaEdicao(); };
  }

  // configurações
  if (tf.vista === "config" && tf.cf) {
    const c = tf.cf;
    todos("[data-cflembrete]", (b) => b.onclick = () => {
      const m = Number(b.dataset.cflembrete), ls = (c.lembretes || []).slice();
      c.lembretes = ls.includes(m) ? ls.filter((x) => x !== m) : ls.concat(m).sort((a, z) => a - z);
      pintaTF();
    });
    todos("[data-cfinsistir]", (b) => b.onclick = () => { c.insistir_min = Number(b.dataset.cfinsistir); pintaTF(); });
    todos("[data-cfcampo]", (el) => el.oninput = el.onchange = () => { c[el.dataset.cfcampo] = el.value; });
    todos("[data-cfbool]", (el) => el.onchange = () => { c[el.dataset.cfbool] = el.checked; });
    q("[data-cfsalvar]").onclick = salvaConfig;
  }
}

// repinta (a data mudou: o horário habilita) sem tirar o foco do campo
function pintaMantendoFoco(sel) {
  pintaTF();
  const el = painelTF.querySelector(sel);
  if (el) el.focus();
}

/* ───────── captura rápida (pranchas 01 e 02) ───────── */

const capAberta = () => !!capTF && !capTF.classList.contains("oculto");

function abrirCaptura(texto) {
  if (!capTF) {
    capTF = document.createElement("div");
    capTF.className = "aparencia tfc oculto";
    capTF.setAttribute("role", "dialog");
    capTF.setAttribute("aria-label", "Anotar tarefa");
    document.body.appendChild(capTF);
  }
  paraContagem();
  if (typeof gravandoVoz === "function" && gravandoVoz() && voz.aoTexto) vozPara(true);
  Object.assign(cap, { etapa: "escrever", texto: texto || "", erro: "", tarefa: null, escolhendo: false });
  if (typeof fecharMenu === "function") fecharMenu();
  capTF.classList.remove("oculto");
  pintaCap();
  const el = capTF.querySelector("#tf-txt");
  if (el) { el.focus(); el.setSelectionRange(el.value.length, el.value.length); }
}

function fecharCaptura() {
  if (!capAberta()) return;
  paraContagem();
  if (typeof gravandoVoz === "function" && gravandoVoz() && voz.aoTexto) vozPara(true);
  capTF.classList.add("oculto");
  if (typeof reportarHit === "function") reportarHit();
}

function htmlCapEscrever() {
  return `
    <div class="ap-topo">${icone("tarefas")}<b>Anotar tarefa</b>
      <button class="botao-x" data-fechar aria-label="Fechar">${icone("fechar", 18)}</button></div>
    <p class="ap-dica">${escTF(nomeAtalho())} · Enter salva · Esc fecha</p>
    <div class="ap-sec"><label class="tf-rot" for="tf-txt">O que precisa ser feito?</label>
      <div class="campo-msg"><textarea id="tf-txt" rows="3" autocomplete="off" placeholder="Ex.: atualizar a conta do PayPal até amanhã às 10h">${escTF(cap.texto)}</textarea></div></div>
    ${cap.erro ? `<div class="ia-status erro" role="status">${escTF(cap.erro)}</div>` : ""}
    <div class="tf-linha">
      <button id="tf-voz" class="voz-mic" aria-label="Gravar áudio"></button>
      <span class="ia-nota tf-cresce">Ou fale: o áudio vira a tarefa</span>
      <button class="ia-bt" data-capsalvar>Salvar</button>
    </div>`;
}

function htmlCapPerguntas() {
  const t = cap.tarefa;
  const topo = `<div class="tf-anotei"><span class="tf-anotei-ok">${icone("ok", 18)}</span>
    <span><span class="tf-rot">Anotei</span><b>${escTF(t.titulo)}</b></span>
    <button class="botao-x" data-fechar aria-label="Fechar">${icone("fechar", 18)}</button></div>`;
  const barra = `<div class="tf-barra" aria-hidden="true"><i id="tf-barra"></i></div>`;
  if (t.data && !cap.escolhendo) {
    // a IA (ou o leitor local) já achou o prazo no texto: só confirmar
    return topo + `<div class="tf-resumo">
        <span>${icone("calendario", 16)}${escTF(maiuscula(quandoTF({ data: t.data })))}${t.data !== hojeTF() && t.data !== somaDias(hojeTF(), 1) ? "" : ", " + escTF(TF_DIA_CURTO[ddia(t.data).getDay()] + " " + ddmm(t.data))}</span>
        <span>${icone("relogio", 16)}${t.hora ? "Entregar até " + escTF(t.hora) : "Sem horário"}</span>
        <span>${icone("sino", 16)}${escTF(textoLembretes(t))}</span>
        ${t.repete && t.repete.tipo ? `<span>${icone("repetir", 16)}Repete ${escTF(textoRepete(t.repete))}</span>` : ""}
      </div>${barra}
      <div class="ia-acoes"><button class="ia-bt sec" data-ajustar>Ajustar</button><button class="ia-bt" data-capok>Está certo</button></div>`;
  }
  const h = hojeTF(), am = somaDias(h, 1);
  const outra = cap.data && cap.data !== h && cap.data !== am;
  return topo + `
    <div class="ap-sec"><span>É para quando?</span>
      <div class="tf-chips">
        ${chipTF(cap.data === h, `data-capdia="${h}"`, "Hoje")}
        ${chipTF(cap.data === am, `data-capdia="${am}"`, "Amanhã")}
        <label class="tf-chip tf-chip-data${outra ? " sel" : ""}">${icone("calendario", 14)}<span>${outra ? escTF(quandoTF({ data: cap.data })) : "Escolher data"}</span>
          <input type="date" data-capdata value="${escTF(cap.data || "")}" aria-label="Escolher data"></label>
        ${chipTF(!cap.data, `data-capdia=""`, "Sem data")}
      </div></div>
    <div class="ap-sec"><span>Tem horário?</span>
      <div class="tf-chips">${chipTF(!cap.hora, "data-semhora", "Sem horário")}
        <label class="tf-rot" for="tf-cap-hora">ou às</label>
        <input class="ia-campo tf-curto" id="tf-cap-hora" type="time" data-caphora value="${escTF(cap.hora || "")}" ${cap.data ? "" : "disabled"}></div></div>
    ${barra}
    <p class="ia-nota" id="tf-conta">Sem resposta, a tarefa fica salva como está. Nada é obrigatório.</p>
    <div class="ia-acoes"><button class="ia-bt sec" data-ajustar>Mais opções</button><button class="ia-bt" data-capok>Pronto</button></div>`;
}

function pintaCap() {
  if (!capTF) return;
  capTF.innerHTML = cap.etapa === "perguntas" ? htmlCapPerguntas()
    : cap.etapa === "anotando" ? `<div class="tf-anotando" role="status"><span class="voz-ponto"></span>Anotando…</div>`
      : htmlCapEscrever();
  const q = (s) => capTF.querySelector(s);
  capTF.querySelectorAll("[data-fechar]").forEach((b) => b.onclick = () => (cap.etapa === "perguntas" ? concluiPerguntas(false) : fecharCaptura()));
  if (cap.etapa === "escrever") {
    const el = q("#tf-txt");
    el.oninput = () => { cap.texto = el.value; };
    el.onkeydown = (e) => {
      if (e.key !== "Enter" || e.shiftKey || e.isComposing) return;
      e.preventDefault();
      salvaCaptura("texto");
    };
    q("[data-capsalvar]").onclick = () => salvaCaptura("texto");
    ligaMic("tf-voz", "tf-txt", (t) => { cap.texto = t; salvaCaptura("audio"); }, capTF);
    if (typeof pintaBotoesVoz === "function") pintaBotoesVoz();
  }
  if (cap.etapa === "perguntas") {
    const mexeu = () => { cap.fim = Date.now() + TF_PERGUNTA_MS; };
    capTF.querySelectorAll("[data-capdia]").forEach((b) => b.onclick = () => {
      cap.data = b.dataset.capdia; if (!cap.data) cap.hora = ""; mexeu(); pintaCap();
    });
    const dt = q("[data-capdata]");
    if (dt) {
      dt.onclick = mexeu;
      dt.onchange = () => { cap.data = dt.value; mexeu(); pintaCap(); };
    }
    if (q("[data-semhora]")) q("[data-semhora]").onclick = () => { cap.hora = ""; mexeu(); pintaCap(); };
    const hr = q("[data-caphora]");
    if (hr) { hr.oninput = hr.onchange = () => { cap.hora = hr.value; mexeu(); }; hr.onfocus = mexeu; }
    q("[data-capok]").onclick = () => concluiPerguntas(true);
    q("[data-ajustar]").onclick = () => {
      const id = cap.tarefa.id;
      concluiPerguntas(true, true).then(() => abrirTarefas("editar", id));
    };
    capTF.onpointerdown = mexeu;
  } else {
    capTF.onpointerdown = null;
  }
  reposiciona(capTF);
}

async function salvaCaptura(origem) {
  const texto = String(cap.texto || "").trim();
  if (!texto || cap.etapa !== "escrever") return;
  cap.etapa = "anotando"; cap.erro = ""; pintaCap();
  const ent = await entendeTF(texto);
  const r = await respTF(G.TarefaCriar(JSON.stringify({ titulo: ent.titulo || texto, data: ent.data, hora: ent.hora, repete: ent.repete, origem, lembretes: null })));
  if (!r.ok) { cap.etapa = "escrever"; cap.erro = fraseUI(r.erro || "não foi possível anotar"); pintaCap(); return; }
  await carregaTF();
  repintaListaTF();
  if (!capAberta()) return; // fechou enquanto anotava: a tarefa já está salva
  if (tf.cfg && tf.cfg.perguntar_prazo === false) {
    fecharCaptura();
    toast("tarefas", "Tarefa anotada", r.tarefa.titulo + " · " + quandoTF(r.tarefa) + ".", 5000, true);
    return;
  }
  Object.assign(cap, { etapa: "perguntas", tarefa: r.tarefa, data: r.tarefa.data || "", hora: r.tarefa.hora || "", escolhendo: !r.tarefa.data });
  pintaCap();
  iniciaContagem();
}

function iniciaContagem() {
  paraContagem();
  cap.fim = Date.now() + TF_PERGUNTA_MS;
  cap.vigia = setInterval(() => {
    const falta = cap.fim - Date.now();
    const b = capTF && capTF.querySelector("#tf-barra");
    if (b) b.style.width = Math.max(0, Math.min(100, (falta / TF_PERGUNTA_MS) * 100)) + "%";
    const n = capTF && capTF.querySelector("#tf-conta");
    if (n) n.textContent = "Sem resposta em " + Math.max(0, Math.ceil(falta / 1000)) + " s, a tarefa fica salva como está. Nada é obrigatório.";
    if (falta <= 0) concluiPerguntas(true);
  }, 250);
}
function paraContagem() { clearInterval(cap.vigia); cap.vigia = null; }

// aplicar = grava o que foi escolhido no cartão (Pronto, tempo esgotado); false = fica como estava (✕)
async function concluiPerguntas(aplicar, quieto) {
  paraContagem();
  const t = cap.tarefa;
  if (!t) { fecharCaptura(); return; }
  let final = t;
  const hora = cap.data ? cap.hora : "";
  if (aplicar && cap.escolhendo && (cap.data !== (t.data || "") || hora !== (t.hora || ""))) {
    const r = await respTF(G.TarefaAtualizar(JSON.stringify(Object.assign({}, t, { data: cap.data, hora }))));
    if (r.ok) final = r.tarefa;
    await carregaTF();
    repintaListaTF();
  }
  fecharCaptura();
  if (!quieto) toast("tarefas", "Tarefa anotada", final.titulo + " · " + quandoTF(final) + ".", 4500, true);
}

/* ───────── sugestão no chat (prancha 03) ───────── */

function tiraSugestoes() { document.querySelectorAll(".tf-sugere").forEach((e) => e.remove()); }

function sugereTarefa(texto) {
  const t = String(texto || "").trim();
  tiraSugestoes();
  if (!t || t.startsWith("/") || !TF_SUGERE.test(t)) return;
  for (const id of ["txt", "cg-txt"]) {
    const campo = document.getElementById(id);
    const rod = campo && campo.closest(".balao-rodape, .cg-rodape");
    if (!rod) continue;
    const s = document.createElement("div");
    s.className = "tf-sugere";
    s.setAttribute("role", "status");
    s.innerHTML = `${icone("tarefas", 16)}<span>Anotar como tarefa?</span>
      <button class="pn-bt" data-sim>Anotar</button><button class="pn-bt sec" data-nao>Agora não</button>`;
    s.querySelector("[data-sim]").onclick = () => {
      tiraSugestoes();
      abrirCaptura(t);
      salvaCaptura("chat");
    };
    s.querySelector("[data-nao]").onclick = tiraSugestoes;
    rod.before(s);
  }
  setTimeout(tiraSugestoes, TF_SUGESTAO_MS);
  if (typeof reportarHit === "function") reportarHit();
}

/* ───────── lembretes e resumo (pranchas 04 e 09) ───────── */

// o toast é um só: os avisos entram numa fila e saem um por vez
function enfileiraAviso(mostra) {
  tf.fila.push(mostra);
  if (!tf.mostrando) proximoAviso();
}
function proximoAviso() {
  const mostra = tf.fila.shift();
  if (!mostra) { tf.mostrando = false; return; }
  tf.mostrando = true;
  mostra();
  const vigia = setInterval(() => {
    if (toastEl.classList.contains("oculto")) { clearInterval(vigia); setTimeout(proximoAviso, 400); }
  }, 800);
}

function mostraLembrete(l) {
  const id = l.tarefa.id, atrasada = l.aviso.tipo === "atrasada";
  const faz = (p) => async () => {
    await respTF(p());
    await carregaTF();
    repintaListaTF();
  };
  const acoes = [{ r: "Feito", prim: true, fn: () => concluiTF(id, true) }];
  if (atrasada) acoes.push({ r: "Adiar 1 h", fn: faz(() => G.TarefaAdiar(id, 60)) }, { r: "Amanhã", fn: faz(() => G.TarefaAdiar(id, -1)) });
  else acoes.push({ r: "Adiar 10 min", fn: faz(() => G.TarefaAdiar(id, 10)) }, { r: "Adiar 1 h", fn: faz(() => G.TarefaAdiar(id, 60)) });
  enfileiraAviso(() => {
    toast(atrasada ? "alerta" : "sino", l.titulo, l.texto, 60000, false, { acoes, clique: () => abrirTarefas("editar", id) });
    try { if (typeof petBipe === "function") petBipe(0.5); } catch (e) { }
  });
}

function mostraResumo(r) {
  const n = r.hoje || 0, at = r.atrasadas || 0;
  const titulo = n ? "Bom dia! Hoje " + (n === 1 ? "é 1 tarefa" : "são " + n + " tarefas") : "Bom dia! Há " + at + (at === 1 ? " tarefa atrasada" : " tarefas atrasadas");
  const partes = [];
  if (r.primeira) partes.push("A primeira é às " + r.primeira.slice(0, 5) + ": " + r.primeira.slice(6) + ".");
  if (n && at) partes.push(at === 1 ? "Uma ficou de antes." : at + " ficaram de antes.");
  const ver = () => abrirTarefas();
  enfileiraAviso(() => toast("tarefas", titulo, partes.join(" ") || "Abra a lista para ver o que está previsto.", 30000, false,
    { acoes: [{ r: "Ver tarefas", prim: true, fn: ver }, { r: "Depois", fn: () => { } }], clique: ver }));
}

/* ───────── início ───────── */

(async function iniciaTF() {
  for (let i = 0; i < 80; i++) {
    if (typeof G !== "undefined" && G && G.TarefasListar && window.runtime && window.runtime.EventsOn) break;
    await new Promise((r) => setTimeout(r, 150));
  }
  if (typeof G === "undefined" || !G || !window.runtime || !window.runtime.EventsOn) return;
  const RT = window.runtime;
  RT.EventsOn("tarefa-lembrete", mostraLembrete);
  RT.EventsOn("tarefas-resumo", mostraResumo);
  RT.EventsOn("captura-tarefa", () => abrirCaptura());
  RT.EventsOn("tarefa-abrir", (id) => abrirTarefas("editar", id));
  RT.EventsOn("tarefas-mudou", async () => {
    await carregaTF();
    repintaListaTF();
  });
  await carregaTF(true);
  setInterval(pintaSeloTF, 60000); // a virada do dia e os prazos que vencem mudam o selo
})();
