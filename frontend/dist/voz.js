/* PetAgent — conversar por áudio (22/10/2026). Pedido da dona: "um botão para gravar
   áudio e mandar áudio pro chat — a ideia é conversar por áudio com o chat".

   🎤 no rodapé do balão e do chat estendido: clique, fale e pare — depois de ~2 s de
   silêncio ele envia sozinho (a seta envia já; o ✕ ou o Esc cancelam). O áudio vai
   para o Go (voz.go), que pede a transcrição ao Groq Whisper; o texto entra no chat pelo
   mesmo enviarTexto() de quem digita.

   🔊 no cabeçalho: liga/desliga a leitura das respostas em voz alta (cfg.vozFala, ligado
   por padrão — escolha da dona: "sempre, com botão liga/desliga"). Usa as vozes pt-BR do
   próprio Windows (speechSynthesis): nada a instalar, nada sai do PC.

   O microfone abre sem diálogo: o Wails libera as permissões do WebView2
   (SetGlobalPermission(Allow)). Sem isso, um pedido de permissão no overlay transparente
   ficaria fora dos retângulos de clique e não teria como ser respondido. */

const VOZ_SILENCIO_MS = 2000;   // pausa que encerra a fala (e envia)
const VOZ_ESPERA_MS = 10000;    // sem falar nada em 10 s: desiste
const VOZ_MAX_MS = 120000;      // teto de uma gravação
const VOZ_BOTOES = [["voz", "txt"], ["cg-voz", "cg-txt"]];

const voz = {
  rec: null, stream: null, ctx: null, vigia: null, pedacos: [], mime: "",
  botao: null, input: null, t0: 0, ultimoSom: 0, falou: false, cancelado: false,
  transcrevendo: false,
};

const gravandoVoz = () => !!(voz.rec && voz.rec.state === "recording");

/* ───────── gravar ───────── */

// aoTexto (opcional): quem recebe o texto transcrito no lugar do chat — a captura de
// tarefas (tarefas.js) usa. Sem ele, o texto vai para a conversa (enviarTexto).
async function vozComeca(botao, input, aoTexto) {
  if ((ocupado && !aoTexto) || voz.transcrevendo || gravandoVoz()) return;
  pararFala();
  try {
    voz.stream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } });
  } catch (e) {
    toast("alerta", "Microfone indisponível", "Não foi possível acessar o microfone (" + (e.name || e) + "). Verifique se ele está conectado e se não está em uso por outro programa.", 8000, true);
    return;
  }
  voz.mime = MediaRecorder.isTypeSupported("audio/webm;codecs=opus") ? "audio/webm;codecs=opus" : "";
  voz.rec = new MediaRecorder(voz.stream, voz.mime ? { mimeType: voz.mime } : undefined);
  voz.mime = voz.rec.mimeType || voz.mime;
  Object.assign(voz, { pedacos: [], botao, input, aoTexto: aoTexto || null, t0: Date.now(), ultimoSom: Date.now(), falou: false, cancelado: false });
  voz.rec.ondataavailable = (e) => { if (e.data && e.data.size) voz.pedacos.push(e.data); };
  voz.rec.onstop = vozTerminou;
  voz.rec.start(250);
  vozVigiaSilencio();
  input.disabled = true;
  pintaBotoesVoz();
  log("voz: gravando");
}

// silêncio encerra a fala: mede o volume a cada 100 ms. O piso de ruído é aprendido no
// primeiro meio segundo (sala silenciosa ≠ escritório com ar-condicionado).
function vozVigiaSilencio() {
  try {
    voz.ctx = new AudioContext();
    const an = voz.ctx.createAnalyser();
    an.fftSize = 1024;
    voz.ctx.createMediaStreamSource(voz.stream).connect(an);
    const buf = new Float32Array(an.fftSize);
    let piso = 0, amostras = 0;
    voz.vigia = setInterval(() => {
      an.getFloatTimeDomainData(buf);
      let soma = 0;
      for (const v of buf) soma += v * v;
      const rms = Math.sqrt(soma / buf.length), agora = Date.now();
      if (agora - voz.t0 < 500) { piso += rms; amostras++; return; }
      const limiar = Math.max(0.012, (amostras ? piso / amostras : 0) * 2.5);
      if (rms > limiar) { voz.falou = true; voz.ultimoSom = agora; }
      pintaNivelVoz(Math.min(1, rms / (limiar * 4)), agora - voz.t0);
      reiniciarAuto(); // gravando: o balão não se esconde sozinho
      if (voz.falou && agora - voz.ultimoSom > VOZ_SILENCIO_MS) vozPara(false);
      else if (!voz.falou && agora - voz.t0 > VOZ_ESPERA_MS) vozPara(true);
      else if (agora - voz.t0 > VOZ_MAX_MS) vozPara(false);
    }, 100);
  } catch (e) {
    // sem medidor de volume: ainda dá para gravar e enviar pelo ■
    voz.falou = true;
  }
}

function vozPara(cancelar) {
  if (!gravandoVoz()) return;
  voz.cancelado = !!cancelar;
  voz.rec.stop(); // → vozTerminou
}

function vozSoltaMicrofone() {
  clearInterval(voz.vigia);
  voz.vigia = null;
  try { voz.ctx && voz.ctx.close(); } catch (e) { }
  voz.ctx = null;
  if (voz.stream) voz.stream.getTracks().forEach((t) => t.stop());
  voz.stream = null;
}

function vozDevolveInput() {
  const input = voz.input;
  if (!input) return;
  input.disabled = false;
}

/* ───────── transcrever e enviar ───────── */

async function vozTerminou() {
  vozSoltaMicrofone();
  const input = voz.input, aoTexto = voz.aoTexto;
  const audio = new Blob(voz.pedacos, { type: voz.mime || "audio/webm" });
  voz.rec = null;
  voz.pedacos = [];
  if (voz.cancelado || !voz.falou || audio.size < 1500) {
    vozDevolveInput();
    pintaBotoesVoz();
    if (!voz.cancelado) toast("microfone", "Nenhuma fala detectada", "Clique no microfone e fale mais perto dele.", 5000, true);
    return;
  }
  voz.transcrevendo = true;
  pintaBotoesVoz();
  let r = { ok: false, erro: "não foi possível transcrever o áudio" };
  try { r = JSON.parse(await G.Transcrever(await blobBase64(audio), audio.type)); } catch (e) { r.erro += ": " + e; }
  voz.transcrevendo = false;
  vozDevolveInput();
  pintaBotoesVoz();
  if (!r.ok) {
    if (r.semChave) {
      toast("microfone", "Voz não configurada", "Para transcrever áudio, cole uma chave gratuita do Groq no painel Inteligência.", 15000, false,
        { acoes: [{ r: "Configurar", prim: true, fn: () => abrirVozNoPainel() }] });
    } else {
      toast("alerta", "Áudio não enviado", fraseUI(r.erro), 9000, true);
    }
    return;
  }
  if (!r.texto) { toast("microfone", "Áudio não compreendido", "Não foi possível identificar palavras no áudio. Tente novamente.", 6000, true); return; }
  if (aoTexto) aoTexto(r.texto);
  else enviarTexto(r.texto, input);
}

function blobBase64(blob) {
  return new Promise((ok, erro) => {
    const fr = new FileReader();
    fr.onload = () => ok(String(fr.result).split(",")[1] || "");
    fr.onerror = () => erro(fr.error);
    fr.readAsDataURL(blob);
  });
}

// sem chave: abre o painel Inteligência já no campo da voz
async function abrirVozNoPainel() {
  if (typeof abrirInteligencia !== "function") return;
  await abrirInteligencia(false);
  const campo = document.querySelector("[data-voz-chave]");
  if (campo) { campo.scrollIntoView({ block: "center" }); campo.focus(); }
}

/* ───────── falar as respostas ───────── */

const falaLigada = () => cfg.vozFala !== false;
let vozPtBR = null;

function escolheVoz() {
  const vs = speechSynthesis.getVoices();
  vozPtBR = vs.find((v) => /pt-BR/i.test(v.lang) && /maria|francisca|thalita/i.test(v.name))
    || vs.find((v) => /pt-BR/i.test(v.lang)) || vs.find((v) => /^pt/i.test(v.lang)) || null;
}
if (window.speechSynthesis) {
  escolheVoz();
  speechSynthesis.onvoiceschanged = escolheVoz;
}

// o que se lê: sem código, link, markdown nem emoji (a voz soletraria tudo isso)
function textoParaFala(t) {
  return String(t || "")
    .replace(/```[\s\S]*?```/g, " (há um trecho de código no chat) ")
    .replace(/`([^`\n]+)`/g, "$1")
    .replace(/https?:\/\/\S+/g, " (link no chat) ")
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s*[-*•]\s+/gm, "")
    .replace(/[*_#>|~]+/g, " ")
    .replace(/\p{Extended_Pictographic}|️|‍/gu, "")
    .replace(/\s+/g, " ")
    .trim();
}

// frases de até ~220 caracteres: uma fala longa demais trava a síntese do Chromium
function frasesParaFala(t) {
  const frases = t.match(/[^.!?;:\n]+[.!?;:]*\s*/g) || [t];
  const blocos = [];
  let atual = "";
  for (const f of frases) {
    if ((atual + f).length > 220 && atual) { blocos.push(atual.trim()); atual = ""; }
    atual += f;
  }
  if (atual.trim()) blocos.push(atual.trim());
  return blocos;
}

function falarResposta(resp) {
  if (!falaLigada() || !window.speechSynthesis || gravandoVoz()) return;
  const t = textoParaFala(resp);
  if (!t || /^(erro:|\(sem resposta\)|\(sem texto\))/i.test(t)) return;
  speechSynthesis.cancel();
  const blocos = frasesParaFala(t);
  blocos.forEach((b, i) => {
    const u = new SpeechSynthesisUtterance(b);
    u.lang = "pt-BR";
    if (vozPtBR) u.voice = vozPtBR;
    u.rate = 1.05;
    if (i === 0) u.onstart = () => classePet("falando");
    if (i === blocos.length - 1) u.onend = u.onerror = () => { if (!ocupado) classePet(estado.state || "idle"); };
    speechSynthesis.speak(u);
  });
}

function pararFala() {
  if (!window.speechSynthesis || !(speechSynthesis.speaking || speechSynthesis.pending)) return;
  speechSynthesis.cancel();
  if (!ocupado) classePet(estado.state || "idle");
}

/* ───────── botões ───────── */

/* Gravando, o campo de texto dá lugar a uma barra: ● Ouvindo 0:04 ▂▅▃ Pare de falar para
   enviar. O 🎤 vira ✕ (cancelar) e a seta de sempre envia o áudio na hora. A barra é
   criada aqui, uma por rodapé, logo depois do campo. */
function barraVoz(input) {
  let b = input.parentElement.querySelector(".voz-barra");
  if (b) return b;
  b = document.createElement("div");
  b.className = "voz-barra";
  b.setAttribute("role", "status");
  b.innerHTML = `<span class="voz-ponto"></span><span class="voz-rotulo"></span>
    <span class="voz-tempo">0:00</span><span class="voz-nivel">${"<i></i>".repeat(5)}</span>
    <span class="voz-dica">Pare de falar para enviar</span>`;
  input.after(b);
  return b;
}

const VOZ_ONDA = [0.55, 0.85, 1, 0.75, 0.5]; // as barrinhas do meio sobem mais
function pintaNivelVoz(nivel, ms) {
  if (!voz.input) return;
  const b = barraVoz(voz.input), s = Math.floor(ms / 1000);
  b.querySelector(".voz-tempo").textContent = Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
  b.querySelectorAll(".voz-nivel i").forEach((i, k) => {
    i.style.height = Math.round(3 + nivel * 15 * VOZ_ONDA[k] * (0.8 + Math.random() * 0.4)) + "px";
  });
}

function pintaBotoesVoz() {
  for (const [idBt, idTxt] of VOZ_BOTOES) {
    const b = document.getElementById(idBt), input = document.getElementById(idTxt);
    if (!b || !input) continue;
    const meu = b === voz.botao, grav = meu && gravandoVoz(), transc = meu && voz.transcrevendo;
    const barra = barraVoz(input), rod = input.parentElement;
    rod.classList.toggle("voz-ouvindo", grav);
    rod.classList.toggle("voz-entendendo", transc);
    barra.querySelector(".voz-rotulo").textContent = transc ? "Transcrevendo o áudio…" : "Ouvindo";
    if (grav) pintaNivelVoz(0, Date.now() - voz.t0);
    b.classList.toggle("gravando", grav);
    b.disabled = transc || (!meu && (gravandoVoz() || voz.transcrevendo));
    b.innerHTML = icone(grav ? "fechar" : "microfone", 18);
    b.title = grav ? "Cancelar o áudio (Esc)" : transc ? "Transcrevendo o áudio…" : "Gravar áudio em vez de digitar";
    b.setAttribute("aria-label", grav ? "Cancelar o áudio" : "Gravar áudio");
  }
  for (const id of ["balao-som", "cg-som"]) {
    const b = document.getElementById(id);
    if (!b) continue;
    const on = falaLigada();
    b.innerHTML = icone(on ? "som" : "mudo", 18);
    b.classList.toggle("sel", on);
    b.setAttribute("aria-pressed", String(on));
    b.title = on ? "Leitura das respostas em voz alta ativada. Clique para silenciar." : "Leitura das respostas em voz alta desativada. Clique para ativar.";
  }
}

VOZ_BOTOES.forEach(([idBt, idTxt]) => {
  const b = document.getElementById(idBt), input = document.getElementById(idTxt);
  if (!b || !input) return;
  b.onclick = () => {
    if (gravandoVoz()) vozPara(true); // gravando, o 🎤 é o ✕: cancela
    else vozComeca(b, input);
  };
});
// gravando, a seta de enviar manda o áudio na hora (fase de captura: chega antes do
// onclick do main.js, que enviaria o campo de texto vazio)
["enviar", "cg-enviar"].forEach((id) => {
  const b = document.getElementById(id);
  if (b) b.addEventListener("click", (e) => {
    if (!gravandoVoz()) return;
    e.stopImmediatePropagation();
    vozPara(false);
  }, true);
});
["balao-som", "cg-som"].forEach((id) => {
  const b = document.getElementById(id);
  if (!b) return;
  b.onclick = () => {
    cfg.vozFala = !falaLigada();
    if (!cfg.vozFala) pararFala();
    salvarPet();
    pintaBotoesVoz();
  };
});
// Esc durante a gravação cancela (antes do Esc do main.js, que fecha o balão)
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && gravandoVoz()) { e.stopImmediatePropagation(); vozPara(true); }
}, true);
pintaBotoesVoz(); // o carregarPet() repinta quando o cfg salvo (com vozFala) chegar
