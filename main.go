package main

import (
	"errors"
	"log"
	"os"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:   "kabuild",
		Usage:  "CLI tool to build ⚒️ images using Kubernetes and deploy to your preferred registry",
		Action: buildDeploy,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "repo",
				Required: true,
				Usage:    "the repository to use",
				Aliases:  []string{"r"},
			},
			&cli.StringFlag{
				Name:    "tag",
				Usage:   "the tag to use",
				Aliases: []string{"t"},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func buildDeploy(ctx *cli.Context) error {
	repoName := ctx.String("repo")
	tag := getOrDefault(ctx.String("tag"), "latest")

	err := BuildKaniko(repoName, tag)
	if err != nil {
		panic(errors.Join(errors.New("error occured while building"), err))
	}
	return nil
}

func getOrDefault(value string, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
