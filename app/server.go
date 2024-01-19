package main

import (
	"fmt"
	"goth-stack-pulumi/app/components"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"
)

func render(c echo.Context, component templ.Component) error {
	return component.Render(c.Request().Context(), c.Response())
}

func main() {
	app := echo.New()

	app.GET("/", func(c echo.Context) error {
		return render(c, components.Welcome("chris"))
	})

	app.Start(":80")

	fmt.Println("hello creature ...")
}
