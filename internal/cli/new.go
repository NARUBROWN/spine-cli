package cli

import (
	_ "embed"
	"strings"
)

//go:embed first_api.md
var firstAPIGuide string

const projectMain = `package main

import (
	"log"

	"{{MODULE}}/internal/application"
)

func main() {
	app, options, err := application.BuildApplication()
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Validate(options); err != nil {
		log.Fatal(err)
	}
	if err := app.Run(options); err != nil {
		log.Fatal(err)
	}
}
`

const projectApplicationTest = `package application

import "testing"

func TestBuildApplicationIsValid(t *testing.T) {
	app, options, err := BuildApplication()
	if err != nil {
		t.Fatalf("generated application registration is invalid: %v", err)
	}
	if err := app.Validate(options); err != nil {
		t.Fatalf("generated application is invalid: %v", err)
	}
}
`

const layeredHealthController = `package controller

import "github.com/NARUBROWN/spine/pkg/httpx"

type HealthController struct{}
func NewHealthController() *HealthController { return &HealthController{} }
func (c *HealthController) Get() httpx.Response[map[string]string] {
 return httpx.Response[map[string]string]{Body: map[string]string{"status":"ok"}}
}
`

const layeredHealthRoute = `package route

import (
 "github.com/NARUBROWN/spine"
 controller "{{MODULE}}/src/controller"
)

func Register(app spine.App) error {
 app.Route("GET", "/health", (*controller.HealthController).Get)
 return nil
}
`

func generatedFirstAPIGuide() string {
	_, guide, _ := strings.Cut(firstAPIGuide, "<!-- existing-project -->")
	return "\n# 첫 HTTP API 만들기\n\n이름과 수량을 검사하고 JSON을 반환하는 POST /products 예제입니다.\n" + strings.ReplaceAll(guide, "example.com/shop", "{{MODULE}}")
}
