// teste-painel-tarefas.js - o painel de tarefas (frontend/dist/tarefas.js) sem abrir o app.
//
// O que confere:
//  - o titulo e a anotacao sao escritos pela dona (ou vem do chat / da IA): nada disso
//    pode virar tag no innerHTML do painel (o overlay enxerga window.go.main.App);
//  - a aba Hoje separa Atrasadas / Hoje / Sem data e conta certo;
//  - o calendario do mes tem os dias certos e marca o atraso;
//  - os textos ("amanha as 10:00", "toda terca", "Lembrar 1 h antes e na hora");
//  - a sugestao "Anotar como tarefa?" so aparece para frases de tarefa.
//
//   node teste-painel-tarefas.js      (sai 0 = passou, 1 = falhou)
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const fonte = fs.readFileSync(path.join(__dirname, "frontend", "dist", "tarefas.js"), "utf8");

// DOM de mentira: so o suficiente para o script carregar (o painel e montado por string)
const elemento = () => ({
  classList: { add() {}, remove() {}, toggle() {}, contains: () => true },
  setAttribute() {}, addEventListener() {}, appendChild() {}, querySelector: () => null,
  querySelectorAll: () => [], style: {},
});
const ctx = vm.createContext({
  console,
  document: { getElementById: () => null, createElement: elemento, querySelectorAll: () => [], body: elemento() },
  window: {},
  setTimeout: () => 0, setInterval: () => 0, clearInterval: () => {},
  icone: (n) => `<svg class="ic" data-nome="${n}"></svg>`,
  fraseUI: (t) => t,
});
vm.runInContext(fonte, ctx, { filename: "tarefas.js" });
const js = (codigo) => vm.runInContext(codigo, ctx);

let falhas = 0;
function confere(cond, msg) {
  if (cond) return;
  falhas++;
  console.log("FALHOU: " + msg);
}

const hoje = js("hojeTF()");
const ontem = js(`somaDias(hojeTF(), -1)`);
const amanha = js(`somaDias(hojeTF(), 1)`);
const ATAQUE = '<img src=x onerror="window.go.main.App.SendChat(1)">';

js(`tf.cfg = { lembretes: [0, 15, 60], hora_sem_horario: "09:00", insistir_min: 30 }`);
ctx.__tarefas = [
  { id: "a1", titulo: ATAQUE, nota: ATAQUE, data: ontem, hora: "17:00", feita: false, lembretes: null, repete: { tipo: "" } },
  { id: "h1", titulo: "Atualizar a conta do PayPal", data: hoje, hora: "23:59", feita: false, lembretes: null, repete: { tipo: "" } },
  { id: "h2", titulo: "Enviar o relatorio", data: hoje, hora: "09:00", feita: true, feita_em: hoje + "T08:52:00", lembretes: null, repete: { tipo: "semanal", dias: [2] } },
  { id: "s1", titulo: "Trocar a senha", data: "", hora: "", feita: false, lembretes: null, repete: { tipo: "" }, origem: "audio" },
  { id: "x1", titulo: '"><script>alert(1)</script>', data: amanha, hora: "", feita: false, lembretes: [], repete: { tipo: "" } },
];
js(`tf.tarefas = __tarefas`);

// 1) nada vira tag (titulo, nota, atributos aria-label/title)
for (const [nome, codigo] of [["Hoje", "htmlHoje()"], ["Semana", "htmlSemana()"], ["Mes", "htmlMes()"], ["Feitas", "htmlFeitas()"], ["Lista", "htmlLista()"]]) {
  const html = js(codigo);
  confere(!/<img|<script/i.test(html), `aba ${nome}: o titulo virou tag`);
  confere(!/onerror="/i.test(html), `aba ${nome}: atributo onerror solto`);
}
js(`abreEdicao("a1")`);
const edicao = js("htmlEdicao()");
confere(!/<img/i.test(edicao) && edicao.includes("&lt;img"), "edicao: o titulo tem de aparecer escapado no campo");
js(`tf.vista = "lista"; tf.ed = null`);

// 2) a aba Hoje agrupa e conta
const hojeHtml = js("htmlHoje()");
const iAtr = hojeHtml.indexOf(">Atrasadas<"), iHoje = hojeHtml.indexOf(">Hoje<"), iSem = hojeHtml.indexOf(">Sem data<");
confere(iAtr >= 0 && iHoje > iAtr && iSem > iHoje, "Hoje: secoes fora de ordem (Atrasadas, Hoje, Sem data)");
const c = js("contagemTF()");
confere(c.pend === 1 && c.feitas === 1 && c.atras === 1, "contagem: " + JSON.stringify(c));
confere(hojeHtml.indexOf("PayPal") < hojeHtml.indexOf("Enviar o relatorio"), "Hoje: a pendente vem antes da feita");
confere(/data-check="h2" checked/.test(hojeHtml), "Hoje: a feita vem marcada");
confere(hojeHtml.includes("anotada por áudio"), "Hoje: a origem por audio aparece");
confere(js("descTarefas()").startsWith("2 pendentes (1 atrasada)"), "menu: " + js("descTarefas()"));

// 3) calendario
js(`tf.mes = { y: 2026, m: 9 }; tf.diaSel = ""`); // outubro de 2026 comeca numa quinta
const mes = js("htmlMes()");
confere((mes.match(/data-dia="2026-10-/g) || []).length === 31, "Mes: outubro tem de ter 31 dias");
confere((mes.match(/tf-cel vazia/g) || []).length === 4, "Mes: 01/10/2026 e quinta (4 casas vazias)");
js(`tf.mes = null`);
const mesAtual = js("htmlMes()");
confere(mesAtual.includes(`data-dia="${ontem}"`) ? /tf-qtd atraso/.test(mesAtual) : true, "Mes: o dia com atrasada fica marcado");

// 4) textos
confere(js(`quandoTF({ data: somaDias(hojeTF(), 1), hora: "10:00" })`) === "amanhã às 10:00", "quandoTF amanha");
confere(js(`quandoTF({ data: "" })`) === "sem data", "quandoTF sem data");
confere(js(`textoRepete({ tipo: "semanal", dias: [2] })`) === "toda terça", "textoRepete terca");
confere(js(`textoRepete({ tipo: "semanal", dias: [6] })`) === "todo sábado", "textoRepete sabado");
confere(js(`textoRepete({ tipo: "mensal", dia_mes: 5 })`) === "todo dia 5", "textoRepete mensal");
confere(js(`textoLembretes({ data: hojeTF(), hora: "10:00", lembretes: null })`) === "Lembrar 1 h antes, 15 min antes e na hora", "textoLembretes: " + js(`textoLembretes({ data: hojeTF(), hora: "10:00", lembretes: null })`));
confere(js(`textoLembretes({ data: hojeTF(), hora: "", lembretes: null })`) === "Lembrar às 09:00 do dia", "textoLembretes sem horario");
confere(js(`duracaoTF(90)`) === "1 h 30", "duracaoTF");

// 5) sugestao no chat
for (const s of ["tenho que mandar a proposta até sexta", "Preciso atualizar a conta do PayPal", "lembra de ligar pra Ana", "não posso esquecer do boleto"]) {
  confere(js(`TF_SUGERE.test(${JSON.stringify(s)})`), "deveria sugerir: " + s);
}
for (const s of ["qual o saldo?", "oi, tudo bem?", "me explica esse PDF"]) {
  confere(!js(`TF_SUGERE.test(${JSON.stringify(s)})`), "nao deveria sugerir: " + s);
}

if (falhas) { console.log(falhas + " falha(s)"); process.exit(1); }
console.log("ok: painel de tarefas");
