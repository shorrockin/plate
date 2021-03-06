package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	logPkg "log"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	templatesExtension = ".plate"
	templatesFolder    = ".plates"
)

var log = logger{
	verbose: true,
	logger:  logPkg.New(os.Stderr, "", 0),
}

type logger struct {
	verbose bool
	logger  *logPkg.Logger
}

func (l logger) Printf(f string, v ...interface{}) {
	if l.verbose {
		l.logger.Printf(f, v...)
	}
}

func (l logger) Fatalf(f string, v ...interface{}) {
	l.logger.Fatalf(f, v...)
}

type plate struct {
	srcPath string
	outPath string
}

func newPlate(srcPath, outPath string) *plate {
	return &plate{
		srcPath: srcPath,
		outPath: outPath,
	}
}

func (p *plate) setup() {
	os.MkdirAll(p.srcPath, 0777)
}

func (p *plate) buildTemplatePath(name string) string {
	filename := fmt.Sprintf("%s%s", name, templatesExtension)
	return path.Join(p.srcPath, filename)
}

func (p *plate) buildOutPath(filepath string) string {
	return path.Join(p.outPath, filepath)
}

func (p *plate) ask(name string) string {
	fmt.Printf("> %s: ", name)

	r := bufio.NewReader(os.Stdin)
	val, err := r.ReadString('\n')
	if err != nil {
		log.Fatalf("%v", err)
	}

	val = strings.TrimSpace(val)

	if val == "" {
		return p.ask(name)
	}

	return val
}

func (p *plate) templateFuncs(args ...string) template.FuncMap {
	vars := make(map[string]string)

	return template.FuncMap{
		"args": func(i int) string {
			if i >= len(args) {
				fmt.Printf("The current template requires Args[%d].\n", i)
				fmt.Printf("Current Args are:\n")
				for index, arg := range args {
					fmt.Printf("  %d: %s\n", index, arg)
				}
				os.Exit(1)
			}

			return args[i]
		},

		"ask": func(name string) string {
			if val, ok := vars[name]; ok {
				return val
			}

			val := p.ask(name)
			vars[name] = val

			return val
		},
	}
}

func (p *plate) openTemplate(name string, args ...string) (*template.Template, error) {
	t := template.New("")
	t.Funcs(p.templateFuncs(args...))

	f, err := os.Open(p.buildTemplatePath(name))
	if err != nil {
		return t, err
	}
	defer f.Close()

	content, err := ioutil.ReadAll(f)
	if err != nil {
		return t, err
	}

	return t.Parse(string(content))
}

func (p *plate) availableTemplates() []string {
	pattern := path.Join(p.srcPath, fmt.Sprintf("*%s", templatesExtension))
	paths, err := filepath.Glob(pattern)
	if err != nil {
		log.Fatalf("%v", err)
	}

	var names []string

	for _, path := range paths {
		name, err := filepath.Rel(p.srcPath, path)
		if err != nil {
			log.Fatalf("%v", err)
		}

		names = append(names, name[0:len(name)-len(templatesExtension)])
	}

	return names
}

func (p *plate) execute(name string, args ...string) error {
	t, err := p.openTemplate(name, args...)
	if err != nil {
		return err
	}

	getContent := func(tpl *template.Template) (string, error) {
		buf := bytes.NewBuffer([]byte{})
		err = tpl.Execute(buf, nil)
		if err != nil {
			return "", err
		}

		return strings.TrimSpace(buf.String()), nil
	}

	isCommand := func(str string) bool {
		return strings.HasPrefix(str, "# ")
	}

	// templates are not processed in order. for this reason it's pretty common that
	// command sets rely on files created and as such we'll iterate over this twice
	// first creating all the files then executing command sets
	for _, tpl := range t.Templates() {
		name := tpl.Name()

		if name != "" && !isCommand(name) {
			tplContent, err := getContent(tpl)
			if err != nil {
				return err
			}

			path := p.buildOutPath(name)
			dir := filepath.Dir(path)
			err = os.MkdirAll(dir, 0777)
			if err != nil {
				return err
			}

			log.Printf("Creating file %s\n", path)
			f, err := os.Create(path)
			if err != nil {
				return err
			}

			io.WriteString(f, tplContent)
		}
	}

	// second iteration for commands
	for _, tpl := range t.Templates() {
		name := tpl.Name()

		if name != "" && isCommand(name) {
			tplContent, err := getContent(tpl)
			if err != nil {
				return err
			}

			log.Printf("Executing command set: %s\n", strings.TrimPrefix(string(name), "# "))
			commands := strings.Split(tplContent, "\n")
			for _, command := range commands {
				log.Printf("\t # %s\n", command)
				args := strings.Split(command, " ")
				if len(args) > 0 {
					cmd := exec.Command(args[0], args[1:]...)
					out := bytes.Buffer{}

					cmd.Stdout = &out
					err := cmd.Run()
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

func chooseTemplate(p *plate) string {
	templates := p.availableTemplates()
	if len(templates) < 1 {
		log.Fatalf("No templates available in %s", p.srcPath)
	}

	fmt.Printf("Available templates:\n\n")
	for i, path := range templates {
		fmt.Printf("  %d - %v\n", i+1, path)
	}

	fmt.Printf("\nChoose your template [1-%d]: ", len(templates))

	var i int
	fmt.Scanf("%d", &i)

	if i < 1 || i > len(templates) {
		return chooseTemplate(p)
	}

	return templates[i-1]
}

func main() {
	var tplName string

	flag.StringVar(&tplName, "t", "", "template name")
	flag.Parse()

	usr, err := user.Current()
	if err != nil {
		log.Fatalf("%v", err)
	}

	templatesPath := path.Join(usr.HomeDir, templatesFolder)

	args := os.Args

	if len(args) < 2 {
		fmt.Printf("Usage:\n  %s PROJECT_PATH\n", args[0])
		os.Exit(1)
	}

	p := newPlate(templatesPath, args[1])
	p.setup()
	name := chooseTemplate(p)
	err = p.execute(name, args...)
	if err != nil {
		log.Fatalf("%v", err)
	}
}
