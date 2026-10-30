// teste-chat-codigo.js - os blocos de codigo (```) do chat viram caixa de codigo, com seguranca.
//
// Por que existe: a resposta do agente (e o que vem de fora, como um PDF resumido) entra no
// innerHTML do chat por `formata`. O codigo dentro do bloco tem de sair como TEXTO (um SQL
// com "<script>" nao pode rodar) e sem virar negrito ("SELECT **" nao e markdown).
//
//   node teste-chat-codigo.js      (sai 0 = passou, 1 = falhou)
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const fonte = fs.readFileSync(path.join(__dirname, "frontend", "dist", "main.js"), "utf8");

function extrai(nome) {
  const m = new RegExp("(async\\s+)?function\\s+" + nome + "\\s*\\(").exec(fonte);
  if (!m) throw new Error("funcao nao achada: " + nome);
  let i = fonte.indexOf("{", m.index), prof = 0;
  for (; i < fonte.length; i++) {
    if (fonte[i] === "{") prof++;
    else if (fonte[i] === "}" && --prof === 0) return fonte.slice(m.index, i + 1);
  }
  throw new Error("funcao sem fim: " + nome);
}
const ling = /const LING_CODIGO = \{[\s\S]*?\n\};/.exec(fonte);
if (!ling) throw new Error("LING_CODIGO nao achado");

const ctx = vm.createContext({ icone: (n) => `<svg data-ic="${n}"></svg>` });
vm.runInContext([ling[0], extrai("escHtml"), extrai("nomeLinguagem"), extrai("formata")].join("\n"), ctx);
const formata = (t) => vm.runInContext("formata(" + JSON.stringify(t) + ")", ctx);

let falhas = 0;
function confere(cond, msg, html) {
  if (cond) return;
  falhas++;
  console.log("FALHOU: " + msg + (html ? "\n   " + html : ""));
}

// 1) bloco com linguagem: caixa, nome e o codigo como texto
let h = formata("Rode isto:\n```sql\nSELECT * FROM contas WHERE nome = '<script>alert(1)</script>' AND **x**;\n```\nPronto.");
confere(h.includes('<div class="bloco-codigo">'), "bloco virou caixa", h);
confere(h.includes("<span>SQL</span>"), "nome da linguagem SQL", h);
confere(!/<script>/i.test(h) && h.includes("&lt;script&gt;"), "codigo escapado", h);
confere(!h.includes("<b>x</b>") && h.includes("**x**"), "nada de negrito dentro do codigo", h);
confere(h.startsWith("Rode isto:<div") && h.endsWith("</div>Pronto."), "quebras de linha em volta do bloco saem", h);

// 2) terminal, sem linguagem e bloco sem fechar (resposta ainda chegando)
confere(formata("```bash\ngit status\n```").includes("<span>Terminal</span>"), "bash = Terminal");
confere(formata("```\nalgo\n```").includes("<span>Código</span>"), "sem linguagem = Código");
h = formata("Veja:\n```powershell\nGet-Process petagent");
confere(h.includes("<span>PowerShell</span>") && h.includes("Get-Process petagent</code></pre>"), "bloco sem fechar", h);

// 3) varios blocos e o texto entre eles continua formatado
h = formata("**Passo 1**\n```cmd\ndir\n```\nDepois `ls` e:\n```json\n{\"a\": 1}\n```");
confere((h.match(/class="bloco-codigo"/g) || []).length === 2, "dois blocos", h);
confere(h.includes("<b>Passo 1</b>") && h.includes("<code>ls</code>"), "negrito e codigo curto fora dos blocos", h);
confere(h.includes("{&quot;a&quot;: 1}"), "json escapado", h);

// 4) o marcador interno nao pode ser forjado pelo texto
h = formata("texto \u00000\u0000 forjado");
confere(!h.includes("bloco-codigo") && !h.includes("undefined"), "marcador forjado", h);

// 5) o resto continua igual
confere(formata("<img src=x onerror=alert(1)>") === "&lt;img src=x onerror=alert(1)&gt;", "texto comum escapado");
confere(formata("# Titulo") === "<b>Titulo</b>", "titulo vira negrito");

if (falhas) { console.log(falhas + " falha(s)"); process.exit(1); }
console.log("ok: blocos de codigo do chat");
