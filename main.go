package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// inst — singleton (usado pelo systray)
var inst *App

// screenSize — resolução física real (o overlay cobre a tela toda)
func screenSize() (int, int) {
	u := syscall.NewLazyDLL("user32.dll")
	p := u.NewProc("GetSystemMetrics")
	w, _, _ := p.Call(0) // SM_CXSCREEN
	h, _, _ := p.Call(1) // SM_CYSCREEN
	if w == 0 || h == 0 {
		return 1920, 1080
	}
	return int(w), int(h)
}

func main() {
	// modo utilitário: petagent balance  → imprime o saldo e sai (teste headless)
	if len(os.Args) > 1 && os.Args[1] == "balance" {
		println(balanceText(0))
		return
	}
	// modo utilitário: petagent monitores → lista telas + área útil (teste headless)
	if len(os.Args) > 1 && os.Args[1] == "monitores" {
		b, _ := json.MarshalIndent(listarMonitores(), "", "  ")
		println(string(b))
		return
	}

	// Modo headless: consultar a galeria pelo terminal (mesma função do painel).
	if len(os.Args) > 1 && os.Args[1] == "galeria" {
		a := NewApp()
		termo := ""
		if len(os.Args) > 2 {
			termo = os.Args[2]
		}
		resp := a.PetGaleria(termo, 0)
		var d PetGaleria
		_ = json.Unmarshal([]byte(resp), &d)
		fmt.Println("total:", d.Total, "| miniaturas:", d.Base)
		for _, it := range d.Itens {
			marca := "  "
			if it.Local {
				marca = "✓ "
			}
			fmt.Println(marca + it.Slug + " — " + it.Nome + " (" + it.Tipo + ")")
		}
		return
	}

	app := NewApp()
	inst = app

	go runTray()

	// MODO DE TESTE (arquivo modo.txt): cheio | pequeno | barra
	// Serve para isolar o que quebra a transparência sem recompilar.
	modo := "cheio"
	if b, err := os.ReadFile(filepath.Join(pastaPetAgente(), "modo.txt")); err == nil {
		if m := strings.TrimSpace(string(b)); m != "" {
			modo = m
		}
	}
	larg, alt := tamanhoJanelaInicial()
	// NUNCA usar Frameless do Wails: ele deixa a janela opaca (fundo branco).
	// Estratégia: janela NORMAL (nasce transparente, barra de título incluída) e
	// a barra é empurrada para fora da tela — ver alinhaCliente().
	frameless := false
	tirarBarra = true
	switch modo {
	case "pequeno":
		larg, alt = 900, 620
	case "barra":
		tirarBarra = false // diagnóstico: barra visível (feio)
	case "sem-barra":
		tirarBarra = true
	case "frameless":
		frameless = true // diagnóstico: fundo branco (ruim)
	}
	logMsg("startup: modo=" + modo + " frameless=" + fmt.Sprint(frameless) +
		" tamanho=" + fmt.Sprint(larg) + "x" + fmt.Sprint(alt))
	modoTeste = modo
	tamanhoAtual[0], tamanhoAtual[1] = larg, alt

	err := wails.Run(&options.App{
		Title:            "PetAgent",
		Width:            larg,
		Height:           alt,
		Frameless:        frameless, // sem barra de título: o boneco mora na tela
		AlwaysOnTop:      true,
		DisableResize:    true,
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		// botão direito com "Copiar"/"Colar": só sobre texto selecionado e nos campos —
		// o CSS (--default-contextmenu) esconde o menu no resto (o boneco tem o dele)
		EnableDefaultContextMenu: true,
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
		},
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   app.startup,
		Bind:        []interface{}{app},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
