package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/portdock/portdock/internal/templates"
)

const templateUsage = `pdctl template — service templates, without a PortDock server

  pdctl template lint FILE...                  check template files (exit 1 on an error)
  pdctl template bundle DIR [-o catalog.json] [--name NAME]
                                               write a catalog of the *.yml templates of DIR
  pdctl template convert FILE [-o OUT]         convert a Coolify template (templates/compose/*.yaml)
                                               or Coolify's service-templates.json (to a catalog)
`

func template(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" {
		fmt.Print(templateUsage)
		return nil
	}
	switch args[0] {
	case "lint":
		return templateLint(args[1:])
	case "bundle":
		return templateBundle(args[1:])
	case "convert":
		return templateConvert(args[1:])
	}
	return fmt.Errorf("unknown template command %q (pdctl template help)", args[0])
}

func slugOf(file string) string {
	base := filepath.Base(file)
	return strings.TrimSuffix(strings.TrimSuffix(base, filepath.Ext(base)), ".compose")
}

func templateLint(files []string) error {
	if len(files) == 0 {
		return errors.New("template lint: give template files")
	}
	failed := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		t, err := templates.Parse(slugOf(f), string(b))
		if err != nil {
			fmt.Printf("%s: error: %v\n", f, err)
			failed++
			continue
		}
		bad := false
		for _, is := range templates.Lint(t) {
			level := "warning"
			if is.Error {
				level, bad = "error", true
			}
			fmt.Printf("%s: %s: %s\n", f, level, is.Text)
		}
		if bad {
			failed++
		} else {
			fmt.Printf("%s: ok (%s, %s)\n", f, t.Name, t.Category)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d templates have errors", failed, len(files))
	}
	return nil
}

func templateBundle(args []string) error {
	var out, name string
	pos, err := parseFlags("template bundle", args, 1, func(fs *flag.FlagSet) {
		fs.StringVar(&out, "o", "catalog.json", "output file")
		fs.StringVar(&name, "name", "", "name of the catalog")
	})
	if err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(pos[0], "*.yml"))
	more, _ := filepath.Glob(filepath.Join(pos[0], "*.yaml"))
	files = append(files, more...)
	sort.Strings(files)
	var items []templates.CatalogItem
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		t, err := templates.Parse(slugOf(f), string(b))
		if err != nil {
			return err
		}
		for _, is := range templates.Lint(t) {
			if is.Error {
				return fmt.Errorf("%s: %s (pdctl template lint)", f, is.Text)
			}
		}
		items = append(items, templates.CatalogItem{Slug: t.Slug, Compose: string(b)})
	}
	if len(items) == 0 {
		return fmt.Errorf("no *.yml templates in %s", pos[0])
	}
	doc, err := templates.Bundle(name, items)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, append(doc, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: %d templates\n", out, len(items))
	return nil
}

func templateConvert(args []string) error {
	var out string
	pos, err := parseFlags("template convert", args, 1, func(fs *flag.FlagSet) { fs.StringVar(&out, "o", "", "output file (default: standard output)") })
	if err != nil {
		return err
	}
	b, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	var result []byte
	if strings.HasSuffix(pos[0], ".json") {
		// The whole Coolify catalog, to a PortDock catalog.
		tpls, skipped, err := templates.ParseCatalog(templates.FormatCoolify, b, "")
		if err != nil {
			return err
		}
		items := make([]templates.CatalogItem, 0, len(tpls))
		for _, t := range tpls {
			items = append(items, templates.CatalogItem{Slug: t.Slug, Compose: t.Compose})
		}
		if result, err = templates.Bundle("coolify", items); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%d templates converted, %d skipped\n", len(tpls), len(skipped))
		for _, s := range skipped {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", s.Slug, s.Reason)
		}
	} else {
		t, err := templates.ConvertCoolify(slugOf(pos[0]), string(b))
		if err != nil {
			return err
		}
		for _, n := range t.Notes {
			fmt.Fprintln(os.Stderr, "note:", n)
		}
		result = []byte(t.Compose)
	}
	if out == "" {
		_, err = os.Stdout.Write(result)
		return err
	}
	return os.WriteFile(out, result, 0o644)
}
