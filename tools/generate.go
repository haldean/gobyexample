package main

import (
    "crypto/sha1"
    "fmt"
    "github.com/russross/blackfriday"
    "io/ioutil"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "strings"
    "text/template"
)

var cacheDir = "/tmp/gobyexample-cache"
var siteDir = "site"

func check(err error) {
    if err != nil {
        panic(err)
    }
}

func ensureDir(dir string) {
    err := os.MkdirAll(dir, 0755)
    check(err)
}

func copyFile(src, dst string) {
    dat, err := ioutil.ReadFile(src)
    check(err)
    err = ioutil.WriteFile(dst, dat, 0644)
    check(err)
}

func pipe(bin string, arg []string, src string) []byte {
    cmd := exec.Command(bin, arg...)
    in, _ := cmd.StdinPipe()
    out, _ := cmd.StdoutPipe()
    cmd.Start()
    in.Write([]byte(src))
    in.Close()
    bytes, _ := ioutil.ReadAll(out)
    err := cmd.Wait()
    check(err)
    return bytes
}

func sha1Sum(s string) string {
    h := sha1.New()
    h.Write([]byte(s))
    b := h.Sum(nil)
    return fmt.Sprintf("%x", b)
}

func mustReadFile(path string) string {
    bytes, err := ioutil.ReadFile(path)
    check(err)
    return string(bytes)
}

func cachedPygmentize(lex string, src string) string {
    ensureDir(cacheDir)
    arg := []string{"-l", lex, "-f", "html"}
    bin := "/usr/local/bin/pygmentize"
    cachePath := cacheDir + "/pygmentize-" + strings.Join(arg, "-") + "-" + sha1Sum(src)
    cacheBytes, cacheErr := ioutil.ReadFile(cachePath)
    if cacheErr == nil {
        return string(cacheBytes)
    }
    renderBytes := pipe(bin, arg, src)
    writeErr := ioutil.WriteFile(cachePath, renderBytes, 0600)
    check(writeErr)
    return string(renderBytes)
}

func markdown(src string) string {
    return string(blackfriday.MarkdownCommon([]byte(src)))
}

func readLines(path string) []string {
    src := mustReadFile(path)
    return strings.Split(src, "\n")
}

func mustGlob(glob string) []string {
    paths, err := filepath.Glob(glob)
    check(err)
    return paths
}

func whichLexer(path string) string {
    if strings.HasSuffix(path, ".go") {
        return "go"
    } else if strings.HasSuffix(path, ".sh") {
        return "console"
    }
    panic("No lexer for " + path)
    return ""
}

func whichSiteDir() {
    dir := os.Getenv("SITEDIR")
    if dir != "" {
        siteDir = dir
    }
}

func debug(msg string) {
    if os.Getenv("DEBUG") == "1" {
        fmt.Fprintln(os.Stderr, msg)
    }
}

var docsPat = regexp.MustCompile("^\\s*(\\/\\/|#)\\s")
var todoPat = regexp.MustCompile("\\/\\/ todo: ")

type Seg struct {
    Docs, DocsRendered     string
    Code, CodeRendered     string
    CodeEmpty, CodeLeading bool
}

type Example struct {
    Id, Name    string
    Segs        [][]*Seg
    NextExample *Example
}

func parseSegs(sourcePath string) []*Seg {
    lines := readLines(sourcePath)
    segs := []*Seg{}
    lastSeen := ""
    for _, line := range lines {
        if line == "" {
            lastSeen = ""
            continue
        }
        if todoPat.MatchString(line) {
            continue
        }
        matchDocs := docsPat.MatchString(line)
        matchCode := !matchDocs
        newDocs := (lastSeen == "") || ((lastSeen != "docs") && (segs[len(segs)-1].Docs != ""))
        newCode := (lastSeen == "") || ((lastSeen != "code") && (segs[len(segs)-1].Code != ""))
        if newDocs || newCode {
            debug("NEWSEG")
        }
        if matchDocs {
            trimmed := docsPat.ReplaceAllString(line, "")
            if newDocs {
                newSeg := Seg{Docs: trimmed, Code: ""}
                segs = append(segs, &newSeg)
            } else {
                segs[len(segs)-1].Docs = segs[len(segs)-1].Docs + "\n" + trimmed
            }
            debug("DOCS: " + line)
            lastSeen = "docs"
        } else if matchCode {
            if newCode {
                newSeg := Seg{Docs: "", Code: line}
                segs = append(segs, &newSeg)
            } else {
                segs[len(segs)-1].Code = segs[len(segs)-1].Code + "\n" + line
            }
            debug("CODE: " + line)
            lastSeen = "code"
        }
    }
    for i, seg := range segs {
        seg.CodeEmpty = (seg.Code == "")
        seg.CodeLeading = (i < (len(segs) - 1))
    }
    return segs
}

func parseAndRenderSegs(sourcePath string) []*Seg {
    segs := parseSegs(sourcePath)
    lexer := whichLexer(sourcePath)
    for _, seg := range segs {
        if seg.Docs != "" {
            seg.DocsRendered = markdown(seg.Docs)
        }
        if seg.Code != "" {
            seg.CodeRendered = cachedPygmentize(lexer, seg.Code)
        }
    }
    return segs
}

func parseExamples() []*Example {
    exampleNames := readLines("examples.txt")
    examples := make([]*Example, 0)
    for _, exampleName := range exampleNames {
        if (exampleName != "") && !strings.HasPrefix(exampleName, "#") {
            example := Example{Name: exampleName}
            exampleId := strings.ToLower(exampleName)
            exampleId = strings.Replace(exampleId, " ", "-", -1)
            exampleId = strings.Replace(exampleId, "/", "-", -1)
            exampleId = strings.Replace(exampleId, "'", "", -1)
            example.Id = exampleId
            example.Segs = make([][]*Seg, 0)
            sourcePaths := mustGlob("examples/" + exampleId + "/*")
            for _, sourcePath := range sourcePaths {
                sourceSegs := parseAndRenderSegs(sourcePath)
                example.Segs = append(example.Segs, sourceSegs)
            }
            examples = append(examples, &example)
        }
    }
    for i, example := range examples {
        if i < (len(examples) - 1) {
            example.NextExample = examples[i+1]
        }
    }
    return examples
}

func renderIndex(examples []*Example) {
    indexTmpl := template.New("index")
    _, err := indexTmpl.Parse(mustReadFile("templates/index.tmpl"))
    check(err)
    indexF, err := os.Create(siteDir + "/index.html")
    check(err)
    indexTmpl.Execute(indexF, examples)
}

func renderExamples(examples []*Example) {
    exampleTmpl := template.New("example")
    _, err := exampleTmpl.Parse(mustReadFile("templates/example.tmpl"))
    check(err)
    for _, example := range examples {
        exampleF, err := os.Create(siteDir + "/" + example.Id)
        check(err)
        exampleTmpl.Execute(exampleF, example)
    }
}

func main() {
    whichSiteDir()
    ensureDir(siteDir)
    copyFile("templates/site.css", siteDir+"/site.css")
    copyFile("templates/favicon.ico", siteDir+"/favicon.ico")
    copyFile("templates/404.html", siteDir+"/404.html")
    examples := parseExamples()
    renderIndex(examples)
    renderExamples(examples)
}
