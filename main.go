package main

import (
	"embed"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
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

func loadFiles() []docFile {
	var docFiles []docFile
	files, err := docs.List("/", false, true)
	if err != nil {
		panic(appError{Err: err, Description: "failed to list files"})
	}
	for _, id := range files {
		id = strings.TrimPrefix(id, "/")
		fd, err := docs.Open(id, 0)
		if err != nil {
			panic(appError{Err: err, Description: "failed to open file " + id})
		}
		defer fd.Close()
		body, err := io.ReadAll(fd)
		if err != nil {
			panic(appError{Err: err, Description: "failed to read file " + id})
		}
		docFiles = append(docFiles, docFile{Id: id, Body: string(body)})
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
		doc.id = docFile.Id
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
				documents[doc.host] = document{slug: doc.host, title: title(doc.host), content: "# " + title(doc.host)}
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

func documentViewer(slug string) template.HTML {
	documents := loadDocuments(loadFiles())
	doc, exists := documents[slug]
	path := documentLocation(documents, doc.slug)
	var breadcrumbs []breadcrumb
	for _, slug := range path {
		d := documents[slug]
		if d.title == "" {
			d.title = slug
		}
		breadcrumbs = append(breadcrumbs, breadcrumb{slug, d.title})
	}

	var headerBuilder strings.Builder
	err := templates.ExecuteTemplate(
		&headerBuilder,
		"header.html",
		struct {
			Path template.HTML
			Id   string
			Slug string
		}{breadcrumbsHTML(breadcrumbs), doc.id, doc.slug},
	)
	if err != nil {
		panic(err)
	}

	viewer := "<main>" + parseDocument(doc.content) + "</main>"

	var simpleChildren []string // Child documents without children
	var children []string       // Child documents with children
	for _, slug := range doc.children {
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

	return template.HTML(createPage("Manesei: "+doc.title, template.HTML(headerBuilder.String())+viewer+links))
	// TODO: automatically update links on rename..., document history, file format, backlinks, related documents
}

func serveViewer() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := documentViewer(r.URL.Path)
		w.Write([]byte(page))
	})
}

// documentForm contains data sent to and received from an HTML editor form.
type documentForm struct {
	Id      string
	Host    string
	Slug    string
	Title   string
	Headers string // JSON map[string]string
	Body    string
}

func serveEditor() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		edit := strings.HasPrefix(r.URL.Path, "/edit/")
		var argument string
		if edit {
			argument = strings.TrimPrefix(r.URL.Path, "/edit/")
		} else {
			argument = strings.TrimPrefix(r.URL.Path, "/new/")
		}
		switch r.Method {
		case http.MethodGet:
			var data documentForm

			if edit {
				documents := loadDocuments(loadFiles())
				for _, doc := range documents {
					if doc.id == argument {
						headers, err := json.Marshal(doc.headers)
						if err != nil {
							panic(err)
						}
						data = documentForm{
							doc.id,
							doc.host,
							doc.slug,
							doc.title,
							string(headers),
							doc.content,
						}
					}
				}
				if data.Id == "" { // Document does not exist.
					http.Redirect(w, r, "/new/", http.StatusTemporaryRedirect)
					return
				}
			} else { // New document
				data.Host = argument
			}

			var pageBuilder strings.Builder
			err := templates.ExecuteTemplate(&pageBuilder, "editor.html", data)
			if err != nil {
				panic(err)
			}
			w.Write([]byte(createPage("Manesei (edit)", template.HTML(pageBuilder.String()))))
		case http.MethodPost:
			data := documentForm{
				r.PostFormValue("Id"),
				r.PostFormValue("Host"),
				r.PostFormValue("Slug"),
				r.PostFormValue("Title"),
				r.PostFormValue("Headers"),
				r.PostFormValue("Body"),
			}

			headersStr := ""

			if data.Headers != "" {
				var headers map[string]string
				err := json.Unmarshal([]byte(data.Headers), &headers)
				if err != nil {
					panic(err)
				}

				for k, v := range headers {
					headersStr += k + ": " + v + "\n"
				}
			}

			fileStr := data.Host + ":" + data.Slug + " " + data.Title + "\n" + headersStr + "\n" + data.Body

			if _, err := docs.Stat(data.Id, false); data.Id == "" || errors.Is(err, atylar.ErrIllegalPath) || errors.Is(err, os.ErrNotExist) {
				// New, random identifier
				data.Id = strings.ReplaceAll(uuid.NewString(), ":", "-")
			}
			file, err := docs.Write(data.Id)
			if err != nil {
				panic(appError{Err: err, Description: "failed to open file"})
			}
			defer file.Close()
			if _, err := file.WriteString(fileStr); err != nil {
				panic(appError{Err: err, Description: "failed to write file"})
			}

			w.Header().Set("Location", "/n/"+data.Slug)
			w.WriteHeader(http.StatusSeeOther)
		default:
			panic(errors.New("wrong method"))
		}
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
	http.Handle("/edit/", handler(serveEditor())) // /edit/id
	http.Handle("/new/", handler(serveEditor()))  // /new/host
	// /history/id/revision

	log.Fatal(http.ListenAndServe(":8000", nil))
}
