package main

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
)

func ObfuscateStrings(gopath string) error {
	return filepath.Walk(gopath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) != GoExtension || info.IsDir() {
			return nil
		}
		if err := stringConstsToVar(path); err != nil {
			return err
		}

		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return nil
		}
		contents, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}

		obfuscator := &stringObfuscator{Contents: contents}
		for _, decl := range file.Decls {
			ast.Walk(obfuscator, decl)
		}
		newCode, err := obfuscator.Obfuscate()
		if err != nil {
			return err
		}
		return ioutil.WriteFile(path, newCode, 0755)
	})
}

type stringObfuscator struct {
	Contents []byte
	Nodes    []*ast.BasicLit
}

func (s *stringObfuscator) Visit(n ast.Node) ast.Visitor {
	if lit, ok := n.(*ast.BasicLit); ok {
		if lit.Kind == token.STRING {
			s.Nodes = append(s.Nodes, lit)
		}
		return nil
	} else if decl, ok := n.(*ast.GenDecl); ok {
		if decl.Tok == token.CONST || decl.Tok == token.IMPORT {
			return nil
		}
	} else if _, ok := n.(*ast.StructType); ok {
		// Avoid messing with annotation strings.
		return nil
	}
	return s
}

func (s *stringObfuscator) Obfuscate() ([]byte, error) {
	sort.Sort(s)

	source := `
        package main
        import "encoding/gob"
		import "os"
        func main() {
            list := []string{}
    `
	for _, n := range s.Nodes {
		source += "list = append(list, " + n.Value + ")\n"
	}
	source += `
			gob.NewEncoder(os.Stdout).Encode(list)
        }
    `
	tempDir, err := ioutil.TempDir("", "string_obfuscator")
	if err != nil {
		return nil, err
	}
	defer func() {
		os.RemoveAll(tempDir)
	}()
	tempFile := filepath.Join(tempDir, "source.go")
	if err := ioutil.WriteFile(tempFile, []byte(source), 0755); err != nil {
		return nil, err
	}

	cmd := exec.Command("go", "run", tempFile)
	cmd.Env = []string{"GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH,
		"GOROOT=" + os.Getenv("GOROOT")}
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var parsed []string
	dec := gob.NewDecoder(&output)
	if err := dec.Decode(&parsed); err != nil {
		return nil, err
	}

	var lastIndex int
	var result bytes.Buffer
	data := s.Contents
	for i, node := range s.Nodes {
		strVal := parsed[i]
		startIdx := node.Pos() - 1
		endIdx := node.End() - 1
		result.Write(data[lastIndex:startIdx])
		result.Write(obfuscatedStringCode(strVal))
		lastIndex = int(endIdx)
	}
	result.Write(data[lastIndex:])
	return result.Bytes(), nil
}

func (s *stringObfuscator) Len() int {
	return len(s.Nodes)
}

func (s *stringObfuscator) Swap(i, j int) {
	s.Nodes[i], s.Nodes[j] = s.Nodes[j], s.Nodes[i]
}

func (s *stringObfuscator) Less(i, j int) bool {
	return s.Nodes[i].Pos() < s.Nodes[j].Pos()
}

func obfuscatedStringCode(str string) []byte {
	var res bytes.Buffer
	res.WriteString("(func() string {\n")
	res.WriteString("mask := []byte(\"")
	mask := make([]byte, len(str))
	for i := range mask {
		mask[i] = byte(rand.Intn(256))
		res.WriteString(fmt.Sprintf("\\x%02x", mask[i]))
	}
	res.WriteString("\")\nmaskedStr := []byte(\"")
	for i, x := range []byte(str) {
		res.WriteString(fmt.Sprintf("\\x%02x", x^mask[i]))
	}
	res.WriteString("\")\nres := make([]byte, ")
	res.WriteString(strconv.Itoa(len(mask)))
	res.WriteString(`)
        for i, m := range mask {
            res[i] = m ^ maskedStr[i]
        }
        return string(res)
        }())`)
	return res.Bytes()
}
