/* PetAgent — aparência (modo, cor, fonte, ícones, cantos) e os ícones de traço.
   Carregado ANTES do main.js: aplica o tema padrão já no primeiro quadro, e o
   carregarPet() reaplica com o que estiver salvo em cfg.aparencia (pets.json).
   Escolha da dona em 22/10/2026 (prancha "PetAgent — Menu novo", opção A):
   sai o verde fixo e os emojis do menu; cada um escolhe as próprias cores.
   Tudo vira variável CSS em :root — o style.css só lê var(--…). */

/* ícones de traço (24×24, estilo Lucide). Um único <path> por ícone: o estilo
   (fina / forte / duotone) é só CSS em cima do mesmo desenho. */
const ICONES = (() => {
  const C = (x, y, r) => `M${x - r} ${y}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0`;
  return {
    alimentar: "M3 11h18M4 11a8 8 0 0 0 16 0M9 7.5c0-1.5 1-2 1-3.5M14 7.5c0-1.5 1-2 1-3.5",
    agua: "M12 21.5a7 7 0 0 0 7-7c0-2-1-3.9-3-5.5s-3.5-4-4-6.5c-.5 2.5-2 4.9-4 6.5C6 10.6 5 12.5 5 14.5a7 7 0 0 0 7 7z",
    exercicio: "M22 12h-4l-3 9L9 3l-3 9H2",
    brincar: C(12, 12, 9.5) + "M5.3 5.3c3.7 3.7 3.7 9.7 0 13.4M18.7 5.3c-3.7 3.7-3.7 9.7 0 13.4",
    dormir: "M20.5 14.5A8.5 8.5 0 1 1 9.5 3.5a6.5 6.5 0 0 0 11 11z",
    status: "M3 3v18h18M8 17v-5M13 17V8M18 17v-3",
    colecao: "M6 3h12l4 6-10 12L2 9zM2 9h20M11 3 8 9l4 12 4-12-3-6",
    cuidar: "M19 14c1.5-1.5 3-3.2 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.8 0-3 .5-4.5 2-1.5-1.5-2.7-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4 3 5.5l7 7z",
    boneco: C(5, 10, 2) + C(9.5, 5.5, 2) + C(14.5, 5.5, 2) + C(19, 10, 2) +
      "M12 12c-3 0-6 3.5-6 6 0 1.5 1.2 2.5 3 2.5 1.2 0 2-.6 3-.6s1.8.6 3 .6c1.8 0 3-1 3-2.5 0-2.5-3-6-6-6z",
    tamanho: "M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7",
    passear: C(6, 19, 2.5) + "M8.5 19h9a3.5 3.5 0 0 0 0-7h-11a3.5 3.5 0 0 1 0-7h9" + C(18, 5, 2.5),
    perfil: C(12, 12, 9.5) + "M8 14s1.5 2 4 2 4-2 4-2M9 9.5h.01M15 9.5h.01",
    interacao: "M4 4l7 17 2.5-7.5L21 11z",
    conversar: "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z",
    janela: "M3 4h18v16H3zM3 9h18M7 6.5h.01",
    email: "M3 5h18v14H3zM3 7l9 6 9-6",
    tela: "M3 4h18v12H3zM8 20h8M12 16v4",
    esconder: "M3 3l18 18M10.6 10.6a2 2 0 0 0 2.8 2.8M9.9 5.1A10 10 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-2.2 3.2M6.6 6.6C3.9 8.3 2 12 2 12s3.5 7 10 7a9.7 9.7 0 0 0 5.4-1.6",
    relogio: C(12, 12, 9) + "M12 7v5l3 2",
    recolher: "M4 14h6v6M20 10h-6V4M14 10l7-7M3 21l7-7",
    balao: "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2zM9.5 7.5l5 5M14.5 7.5l-5 5",
    desligar: "M12 2v10M18.4 6.6a9 9 0 1 1-12.8 0",
    ajustes: "M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6",
    seta: "M9 6l6 6-6 6",
    fechar: "M18 6 6 18M6 6l12 12",
    menos: "M5 12h14",
    enviar: "M5 12h14M13 6l6 6-6 6",
    copiar: "M9 9h11v11H9zM5 15H4V4h11v1",
    ok: "M20 6 9 17l-5-5",
    inteligencia: "M12 3l1.9 5.8L20 11l-6.1 2.2L12 19l-1.9-5.8L4 11l6.1-2.2zM19 3v4M17 5h4",
    buscar: "M10.5 3a7.5 7.5 0 1 0 0 15a7.5 7.5 0 1 0 0-15M21 21l-5.2-5.2",
    atualizar: "M21 12a9 9 0 1 1-3-6.7L21 8M21 3v5h-5",
    alerta: "M12 3l10 18H2zM12 10v4M12 17h.01",
    sino: "M6 16v-5a6 6 0 0 1 12 0v5l2 2H4zM10 21h4",
    baixar: "M12 3v12M7 10l5 5 5-5M4 21h16",
    moeda: C(12, 12, 9) + "M12 7v10M9.5 9.5h3.75a1.75 1.75 0 0 1 0 3.5h-2.5a1.75 1.75 0 0 0 0 3.5H14.5",
    foco: C(12, 12, 9) + C(12, 12, 5) + C(12, 12, 1),
    microfone: "M12 2a3 3 0 0 0-3 3v7a3 3 0 0 0 6 0V5a3 3 0 0 0-3-3zM19 10v2a7 7 0 0 1-14 0v-2M12 19v3",
    som: "M11 5 6 9H2v6h4l5 4zM15.5 8.5a5 5 0 0 1 0 7M19 5a10 10 0 0 1 0 14",
    mudo: "M11 5 6 9H2v6h4l5 4zM22 9l-6 6M16 9l6 6",
    // conversas e anexos (prancha "PetAgent — Conversas e Anexos", 22/10/2026)
    adicionar: "M12 5v14M5 12h14",
    lapis: "M4 20h4L19 9l-4-4L4 16zM14 6l4 4",
    reticencias: C(5, 12, 1) + C(12, 12, 1) + C(19, 12, 1),
    clipe: "M21.4 11.1l-9.2 9.2a6 6 0 0 1-8.5-8.5l9.2-9.2a4 4 0 0 1 5.7 5.7l-9.2 9.2a2 2 0 0 1-2.8-2.8l8.5-8.5",
    pasta: "M3 6h6l2 2h10v11H3z",
    etiqueta: "M20.6 13.4l-7.2 7.2a2 2 0 0 1-2.8 0L3 13V3h10l7.6 7.6a2 2 0 0 1 0 2.8zM7.5 7.5h.01",
    lixeira: "M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3",
    imagem: "M3 4h18v16H3zM3 16l5-5 4 4 3-3 6 6" + C(15.5, 8.5, 1.5),
    arquivo: "M14 3H6v18h12V7zM14 3v4h4M9 13h6M9 17h4",
    subir: "M12 15V3M7 8l5-5 5 5M4 15v6h16v-6",
    lista: "M4 6h16M4 12h16M4 18h16",
    abaixo: "M6 9l6 6 6-6",
    // tarefas (27/10/2026)
    tarefas: "M3 17l2 2 4-4M3 7l2 2 4-4M13 6h8M13 12h8M13 18h8",
    calendario: "M5 4h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM16 2v4M8 2v4M3 10h18",
    repetir: "M17 2l4 4-4 4M3 11v-1a4 4 0 0 1 4-4h14M7 22l-4-4 4-4M21 13v1a4 4 0 0 1-4 4H3",
    voltar: "M15 18l-6-6 6-6",
  };
})();

function icone(nome, tam) {
  const t = tam || 20;
  return `<svg class="ic" width="${t}" height="${t}" viewBox="0 0 24 24" aria-hidden="true"><path d="${ICONES[nome] || ""}"/></svg>`;
}

// botões do HTML marcados com data-ic="nome" ganham o ícone (o texto, se houver, fica depois)
function pintaIconesFixos() {
  document.querySelectorAll("[data-ic]").forEach((el) => {
    if (el.querySelector("svg.ic")) return;
    el.insertAdjacentHTML("afterbegin", icone(el.dataset.ic, el.tagName === "SPAN" ? 15 : 18));
  });
}
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", pintaIconesFixos);
else pintaIconesFixos();

const APARENCIA_PADRAO = { modo: "escuro", acento: "#5B6CFF", fonte: "inter", icones: "fina", cantos: "suaves" };

const FONTES = {
  segoe:    { rotulo: "Segoe UI", fam: "'Segoe UI Variable','Segoe UI',system-ui,sans-serif" },
  inter:    { rotulo: "Inter", fam: "Inter,'Segoe UI',system-ui,sans-serif" },
  nunito:   { rotulo: "Nunito", fam: "Nunito,'Segoe UI',system-ui,sans-serif" },
  fraunces: { rotulo: "Fraunces (serifada)", fam: "Fraunces,Georgia,serif" },
  mono:     { rotulo: "JetBrains Mono", fam: "'JetBrains Mono',Consolas,monospace" },
};
const ACENTOS = [
  ["#5B6CFF", "Índigo"], ["#E5603F", "Coral"], ["#D6407F", "Framboesa"],
  ["#D9961A", "Âmbar"], ["#2F7FE8", "Azul"], ["#626873", "Grafite"],
];
const MODOS = [["claro", "Claro"], ["escuro", "Escuro"], ["sistema", "Sistema"]];
const ESTILOS_ICONE = {
  fina:  { rotulo: "Fina", sw: 1.6, fill: "none", fo: 0 },
  forte: { rotulo: "Forte", sw: 2.3, fill: "none", fo: 0 },
  duo:   { rotulo: "Duotone", sw: 1.7, fill: "var(--acento)", fo: 0.3 },
};
const CANTOS = {
  retos:    { rotulo: "Retos", painel: 8, botao: 6, interno: 4 },
  suaves:   { rotulo: "Suaves", painel: 16, botao: 12, interno: 8 },
  redondos: { rotulo: "Redondos", painel: 26, botao: 20, interno: 14 },
};
const PALETAS = {
  escuro: {
    fundo: "#18191D", sup: "#24252B", sup2: "#2E3037", elev: "#383A42", entrada: "#111215",
    texto: "#F2F2F5", sub: "#A9ABB6", borda: "rgba(255,255,255,0.10)", hover: "rgba(255,255,255,0.07)", rolagem: "rgba(255,255,255,0.20)",
    sombra: "0 24px 60px rgba(0,0,0,0.50), 0 2px 8px rgba(0,0,0,0.30)",
    sombraAba: "0 1px 2px rgba(0,0,0,0.5)", perigo: "#FF8A7A",
  },
  claro: {
    fundo: "#FFFFFF", sup: "#F3F3F6", sup2: "#E8E8ED", elev: "#FFFFFF", entrada: "#FFFFFF",
    texto: "#1A1B1F", sub: "#5D5F6A", borda: "rgba(20,20,35,0.12)", hover: "rgba(20,20,35,0.06)", rolagem: "rgba(20,20,35,0.25)",
    sombra: "0 24px 60px rgba(20,20,45,0.22), 0 2px 8px rgba(20,20,45,0.10)",
    sombraAba: "0 1px 3px rgba(20,20,40,0.18)", perigo: "#C2362A",
  },
};

function corRgb(h) {
  h = String(h || "").replace("#", "");
  if (h.length === 3) h = h.split("").map((x) => x + x).join("");
  const n = parseInt(h, 16) || 0;
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}
function luminancia(rgb) {
  const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
  return 0.2126 * f(rgb[0]) + 0.7152 * f(rgb[1]) + 0.0722 * f(rgb[2]);
}

const midiaEscuro = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;

// a aparência salva (cfg mora no main.js e só existe depois que ele carrega)
function aparenciaAtual() {
  const salva = (typeof cfg !== "undefined" && cfg.aparencia) || {};
  return Object.assign({}, APARENCIA_PADRAO, salva);
}

function aplicaAparencia(ap) {
  ap = Object.assign({}, APARENCIA_PADRAO, ap || aparenciaAtual());
  let modo = ap.modo;
  if (modo === "sistema") modo = midiaEscuro && !midiaEscuro.matches ? "claro" : "escuro";
  const esc = modo === "escuro";
  const p = PALETAS[modo];
  const rgb = corRgb(ap.acento), la = luminancia(rgb);
  const ic = ESTILOS_ICONE[ap.icones] || ESTILOS_ICONE.fina;
  const ct = CANTOS[ap.cantos] || CANTOS.suaves;
  const v = {
    "--fundo": p.fundo, "--sup": p.sup, "--sup2": p.sup2, "--elev": p.elev, "--entrada": p.entrada,
    "--texto": p.texto, "--sub": p.sub, "--borda": p.borda, "--hover": p.hover, "--rolagem": p.rolagem,
    "--sombra": p.sombra, "--sombra-aba": p.sombraAba, "--perigo": p.perigo,
    "--acento": ap.acento,
    "--acento-txt": la > 0.42 ? "#15161A" : "#FFFFFF",
    "--acento-suave": `rgba(${rgb[0]},${rgb[1]},${rgb[2]},${esc ? 0.22 : 0.14})`,
    // ícone na cor de destaque — menos quando ela some no fundo (âmbar no claro, grafite no escuro)
    "--icone": (!esc && la > 0.3) || (esc && la < 0.06) ? p.texto : ap.acento,
    "--fonte": (FONTES[ap.fonte] || FONTES.inter).fam,
    "--ic-sw": String(ic.sw), "--ic-fill": ic.fill, "--ic-fo": String(ic.fo),
    "--raio-painel": ct.painel + "px", "--raio-botao": ct.botao + "px", "--raio-interno": ct.interno + "px",
  };
  const raiz = document.documentElement;
  for (const k in v) raiz.style.setProperty(k, v[k]);
  raiz.dataset.tema = modo;
}

// "seguir o sistema": o Windows trocou de claro para escuro (ou o contrário)
if (midiaEscuro) {
  const muda = () => { if (aparenciaAtual().modo === "sistema") aplicaAparencia(); };
  if (midiaEscuro.addEventListener) midiaEscuro.addEventListener("change", muda);
  else if (midiaEscuro.addListener) midiaEscuro.addListener(muda);
}
aplicaAparencia(APARENCIA_PADRAO);

/* ───────── painel Aparência (menu › Sistema › Aparência) ───────── */
let painelAparencia = null;

function mudaAparencia(campo, valor) {
  if (typeof cfg === "undefined") return;
  cfg.aparencia = Object.assign({}, aparenciaAtual(), { [campo]: valor });
  aplicaAparencia(cfg.aparencia);
  if (typeof salvarPet === "function") salvarPet();
  pintaAparencia();
}

function pintaAparencia() {
  const p = painelAparencia;
  if (!p) return;
  const ap = aparenciaAtual();
  const seg = (campo, lista) => `<div class="ap-seg">${lista.map(([v, r]) =>
    `<button data-campo="${campo}" data-valor="${v}" class="${ap[campo] === v ? "sel" : ""}" aria-pressed="${ap[campo] === v}">${r}</button>`).join("")}</div>`;
  const acentoLivre = !ACENTOS.some(([h]) => h.toLowerCase() === String(ap.acento).toLowerCase());
  p.innerHTML = `
    <div class="ap-topo">${icone("ajustes")}<b>Aparência</b>
      <button class="botao-x" data-fechar aria-label="Fechar">${icone("fechar", 18)}</button></div>
    <p class="ap-dica">Vale para o menu, o chat e os painéis.</p>
    <div class="ap-sec"><span>Modo</span>${seg("modo", MODOS)}</div>
    <div class="ap-sec"><span>Cor de destaque</span>
      <div class="ap-cores">${ACENTOS.map(([h, r]) =>
        `<button class="ap-cor ${h.toLowerCase() === String(ap.acento).toLowerCase() ? "sel" : ""}" data-campo="acento" data-valor="${h}" style="background:${h}" aria-label="${r}" title="${r}"></button>`).join("")}
        <label class="ap-livre ${acentoLivre ? "sel" : ""}"><input type="color" value="${ap.acento}" />Outra</label>
      </div></div>
    <div class="ap-sec"><span>Fonte</span><div class="ap-fontes">${Object.entries(FONTES).map(([k, f]) =>
      `<button data-campo="fonte" data-valor="${k}" class="${ap.fonte === k ? "sel" : ""}" style="font-family:${f.fam.replace(/"/g, "'")}"><i>Aa</i>${f.rotulo}</button>`).join("")}</div></div>
    <div class="ap-sec"><span>Ícones</span>${seg("icones", Object.entries(ESTILOS_ICONE).map(([k, e]) => [k, e.rotulo]))}
      <div class="ap-amostra">${["alimentar", "agua", "conversar", "email", "desligar"].map((n) => icone(n, 22)).join("")}</div></div>
    <div class="ap-sec"><span>Cantos</span>${seg("cantos", Object.entries(CANTOS).map(([k, c]) => [k, c.rotulo]))}</div>`;
  p.querySelectorAll("[data-campo]").forEach((b) => {
    b.onclick = () => mudaAparencia(b.dataset.campo, b.dataset.valor);
  });
  const livre = p.querySelector("input[type=color]");
  // input = arrastando no seletor (só aplica); change = soltou (salva)
  livre.oninput = () => aplicaAparencia(Object.assign({}, ap, { acento: livre.value }));
  livre.onchange = () => mudaAparencia("acento", livre.value);
  p.querySelector("[data-fechar]").onclick = fecharAparencia;
  if (typeof reportarHit === "function") reportarHit();
}

function abrirAparencia() {
  if (!painelAparencia) {
    painelAparencia = document.createElement("div");
    painelAparencia.className = "aparencia oculto";
    document.body.appendChild(painelAparencia);
  }
  painelAparencia.classList.remove("oculto");
  pintaAparencia();
  if (typeof posicionaPertoDoPet === "function") posicionaPertoDoPet(painelAparencia);
  if (typeof reportarHit === "function") reportarHit();
}
function fecharAparencia() {
  if (!painelAparencia) return;
  painelAparencia.classList.add("oculto");
  if (typeof reportarHit === "function") reportarHit();
}
