package main

import (
	"embed"
	"errors"
	"html/template"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/atmatto/atylar"
)

//go:embed fonts/*
var fontsFS embed.FS

//go:embed templates/*
var templatesFS embed.FS
var templates = template.Must(template.ParseFS(templatesFS, "templates/*"))

// createPage returns a full HTML document
func createPage(title string, body template.HTML) string {
	var s strings.Builder
	err := templates.ExecuteTemplate(&s, "app.html", struct {
		Title string
		Body  template.HTML
	}{title, body})
	if err != nil {
		panic(err)
	}
	return s.String()
}

var dataDirectory = "notes"
var docs atylar.Store

type appError struct {
	Err         error  // The real error, not reported to the client
	Description string // What will be reported in the http response
	Status      int    // Status code to use in response (Default: 500)
}

func (err *appError) Error() string {
	if err.Err == nil {
		return err.Description
	} else if err.Description == "" {
		return err.Err.Error()
	} else {
		return "(" + err.Description + ") " + err.Err.Error()
	}
}

// handler does error reporting
func handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			r := recover()
			if r != nil {
				if err, ok := r.(appError); ok {
					if err.Err != nil {
						log.Println(err)
					}
					status := http.StatusInternalServerError
					if err.Status != 0 {
						status = err.Status
					}
					if perr, ok := err.Err.(*os.PathError); ok {
						err.Description += " (" + perr.Op + ": " + perr.Err.Error() + ")"
					} else if errors.Is(err.Err, atylar.ErrIllegalPath) {
						err.Description += " (illegal path)"
					}
					http.Error(w, err.Description, status)
				} else {
					log.Println(r)
					http.Error(w, "Unknown error ("+log.Prefix()+")", http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type docFile struct {
	Id   string `json:"id"`             // Identifier
	Body string `json:"body,omitempty"` // Content
}

//func serveDocs() http.Handler {
//	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		if r.Method == http.MethodPut {
//			// If the specified file already exists, update it, otherwise
//			// create a new file with a random identifier. On success respond
//			// with a doc object with the used identifier but without the body.
//
//			var request docFile
//			requestBytes, err := io.ReadAll(r.Body)
//			if err != nil {
//				panic(appError{Err: err, Description: "failed to read request"})
//			}
//			if err := json.Unmarshal(requestBytes, &request); err != nil {
//				panic(appError{err, "failed to parse request: " + err.Error(), http.StatusBadRequest})
//			}
//			// If the body is completly empty, it means
//			// that the file should be deleted.
//			if request.Body == "" {
//				err = docs.Remove(request.Id)
//				if err != nil {
//					panic(appError{Err: err, Description: "failed to remove file"})
//				}
//				return
//			}
//			if _, err := docs.Stat(request.Id, false); errors.Is(err, atylar.ErrIllegalPath) || errors.Is(err, os.ErrNotExist) {
//				request.Id = strings.ReplaceAll(uuid.NewString(), ":", "-")
//			}
//			file, err := docs.Write(request.Id)
//			if err != nil {
//				panic(appError{Err: err, Description: "failed to open file"})
//			}
//			defer file.Close()
//			if _, err := file.WriteString(request.Body); err != nil {
//				panic(appError{Err: err, Description: "failed to write file"})
//			}
//			responseBytes, err := json.Marshal(docFile{Id: request.Id})
//			if err != nil {
//				panic(appError{Err: err, Description: "failed to marhsall response"})
//			}
//			_, err = w.Write(responseBytes)
//			if err != nil {
//				log.Println("Failed to write reponse", err)
//			}
//		} else {
//			panic(appError{Description: "unsupported method", Status: http.StatusBadRequest})
//		}
//	})
//}

func loadFiles() []docFile {
	var docFiles []docFile
	files, err := docs.List("/", false, true)
	if err != nil {
		panic(appError{Err: err, Description: "failed to list files"})
	}
	for _, file := range files {
		file = strings.TrimPrefix(file, "/") // TODO: Why was this added?
		fd, err := docs.Open(file, 0)
		if err != nil {
			panic(appError{Err: err, Description: "failed to open file " + file})
		}
		defer fd.Close()
		body, err := io.ReadAll(fd)
		if err != nil {
			panic(appError{Err: err, Description: "failed to read file " + file})
		}
		docFiles = append(docFiles, docFile{Id: file, Body: string(body)})
	}
	return docFiles
}

type document struct {
	id          string
	host        string
	slug        string
	isDuplicate bool   // An exactly named document already exists
	duplicateOf string // In case of a duplicate, this stores the original slug
	title       string
	headers     map[string]string
	content     string
	children    []string // Children's slugs
}

func (host document) addChild(slug string) document {
	for _, child := range host.children {
		if child == slug {
			return host
		}
	}
	host.children = append(host.children, slug)
	return host
}

func title(str string) string {
	if len(str) < 1 {
		return str
	}
	first := string(str[0])
	tail := str[1:]
	return strings.ToTitle(first) + tail
}

func loadDocuments(docFiles []docFile) map[string]document {
	documents := map[string]document{
		"": {title: "🌱", content: "# Manesei"},
	}

	for _, docFile := range docFiles {
		// TODO: Safety!
		doc := document{}
		lines := strings.Split(docFile.Body, "\n")
		head := strings.SplitN(lines[0], " ", 2)
		doc.slug = strings.SplitN(head[0], ":", 2)[1]
		if _, ok := documents[doc.slug]; ok {
			doc = documents[doc.slug]
		}
		doc.host = strings.SplitN(head[0], ":", 2)[0]
		doc.title = head[1]

		// If host and slug are equal to "", then
		// the document is the root document.

		if doc.host == doc.slug {
			// In case of a cyclic reference, the
			// root document is set to be the host.
			doc.host = ""
		}

		counter := 1
		for _, line := range lines[1:] {
			counter++
			if strings.TrimSpace(line) == "" {
				break
			}
			h := strings.SplitN(line, ":", 2)
			if len(h) == 1 {
				break
			}
			if doc.headers == nil {
				doc.headers = make(map[string]string)
			}
			doc.headers[strings.TrimSpace(h[0])] = strings.TrimSpace(h[1])
		}
		doc.content = strings.Join(lines[counter:], "\n")

		// If an exactly named document already exists, then the
		// slug of the current name is changed and the original
		// slug is saved in the `duplicateOf` field.
		if d, ok := documents[doc.slug]; ok && d.id != "" && d.id != docFile.Id {
			doc.isDuplicate = true
			doc.duplicateOf = doc.slug
			doc.slug += "-duplicate"
			for d, ok := documents[doc.slug]; ok && d.id != "" && d.id != docFile.Id; d, ok = documents[doc.slug] {
				doc.slug += string("abcdefghijklmnopqrstuvwxyz"[rand.Intn(26)])
			}
		}

		documents[doc.slug] = doc
	}

	// Connections
	for _, doc := range documents {
		if doc.host != doc.slug {
			if _, ok := documents[doc.host]; !ok {
				// This way, even if the host document doesn't exist,
				// the current document will be accessible, because
				// the placeholder host will be a child of the root
				// document.
				documents[doc.host] = document{slug: doc.host, title: doc.host, content: "# " + title(doc.host)}
				documents[""] = documents[""].addChild(doc.host)
			}
			documents[doc.host] = documents[doc.host].addChild(doc.slug)
		}
	}

	return documents
}

func documentLocation(documents map[string]document, slug string) []string {
	if documents[slug].host != slug {
		return append(documentLocation(documents, documents[slug].host), slug)
	}
	if slug == "" {
		return []string{}
	} else {
		return []string{slug}
	}
}

type breadcrumb struct {
	slug  string
	title string
}

func breadcrumbsHTML(breadcrumbs []breadcrumb) template.HTML {
	str := `<div class="path"><a class="root" href="/n/">🌱</a>`
	for _, b := range breadcrumbs {
		str += ` / <a href="` + b.slug + `">` + b.title + `</a>`
	}
	str += `</div>`
	return template.HTML(str)
}

func viewer(slug string) template.HTML {
	documents := loadDocuments(loadFiles())
	document, exists := documents[slug]
	path := documentLocation(documents, document.slug)
	var breadcrumbs []breadcrumb
	for _, slug := range path {
		doc := documents[slug]
		if doc.title == "" {
			doc.title = slug
		}
		breadcrumbs = append(breadcrumbs, breadcrumb{slug, doc.title})
	}

	var headerBuilder strings.Builder
	err := templates.ExecuteTemplate(&headerBuilder, "header.html", struct{ Path template.HTML }{breadcrumbsHTML(breadcrumbs)})
	if err != nil {
		panic(err)
	}

	viewer := template.HTML("<main>" + strings.ReplaceAll(document.content, "\n", "<br>") + "</main>")

	var simpleChildren []string // Child documents without children
	var children []string       // Child documents with children
	for _, slug := range document.children {
		if len(documents[slug].children) == 0 {
			simpleChildren = append(simpleChildren, slug)
		} else {
			children = append(children, slug)
		}
	}
	links := template.HTML(`<ul class="links">`)
	for _, slug := range simpleChildren {
		links += template.HTML(`<li><a class="file" href="` + slug + `">` + documents[slug].title + `</a></li>`)
	}
	links += `</ul>`
	for _, slug := range children {
		links += template.HTML(`<a class="file" href="` + slug + `">` + documents[slug].title + `</a><ul class="links">`)
		for _, child := range documents[slug].children {
			links += template.HTML(`<li><a class="file" href="` + child + `">` + documents[child].title + `</a></li>`)
		}
		links += `</ul>`
	}

	if !exists {
		viewer = "<main><h2>This document does not exist.</h2></main>"
	}

	return template.HTML(createPage("Manesei: "+document.title, template.HTML(headerBuilder.String())+viewer+links))
}

func serveViewer() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := viewer(r.URL.Path)
		w.Write([]byte(page))
	})
}

func main() {
	var err error
	if docs, err = atylar.New(dataDirectory); err != nil {
		log.Fatal("Couldn't init storage.")
	}

	http.Handle("/", handler(http.RedirectHandler("/n/", http.StatusTemporaryRedirect)))
	http.Handle("/fonts/", http.FileServer(http.FS(fontsFS)))
	http.Handle("/n/", http.StripPrefix("/n/", handler(serveViewer())))

	log.Fatal(http.ListenAndServe(":8000", nil))
}
