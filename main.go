package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/atmatto/atylar"
	"github.com/google/uuid"
)

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

type doc struct {
	Id   string `json:"id"`             // Identifier
	Body string `json:"body,omitempty"` // Content
}

func serveDocs() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Respond with a list of all files and their contents.

			response := []doc{}
			files, err := docs.List("/", false, true)
			if err != nil {
				panic(appError{Err: err, Description: "failed to list files"})
			}
			for _, file := range files {
				file = strings.TrimPrefix(file, "/")
				fd, err := docs.Open(file, 0)
				if err != nil {
					panic(appError{Err: err, Description: "failed to open file " + file})
				}
				defer fd.Close()
				body, err := io.ReadAll(fd)
				if err != nil {
					panic(appError{Err: err, Description: "failed to read file " + file})
				}
				response = append(response, doc{Id: file, Body: string(body)})
			}
			responseBytes, err := json.Marshal(response)
			if err != nil {
				panic(appError{Err: err, Description: "failed to marhsall response"})
			}
			_, err = w.Write(responseBytes)
			if err != nil {
				log.Println("Failed to write reponse", err)
			}
		} else if r.Method == http.MethodPut {
			// If the specified file already exists, update it, otherwise
			// create a new file with a random identifier. On success respond
			// with a doc object with the used identifier but without the body.

			var request doc
			requestBytes, err := io.ReadAll(r.Body)
			if err != nil {
				panic(appError{Err: err, Description: "failed to read request"})
			}
			if err := json.Unmarshal(requestBytes, &request); err != nil {
				panic(appError{err, "failed to parse request: " + err.Error(), http.StatusBadRequest})
			}
			// If the body is completly empty, it means
			// that the file should be deleted.
			if request.Body == "" {
				err = docs.Remove(request.Id)
				if err != nil {
					panic(appError{Err: err, Description: "failed to remove file"})
				}
				return
			}
			if _, err := docs.Stat(request.Id, false); errors.Is(err, atylar.ErrIllegalPath) || errors.Is(err, os.ErrNotExist) {
				request.Id = strings.ReplaceAll(uuid.NewString(), ":", "-")
			}
			file, err := docs.Write(request.Id)
			if err != nil {
				panic(appError{Err: err, Description: "failed to open file"})
			}
			defer file.Close()
			if _, err := file.WriteString(request.Body); err != nil {
				panic(appError{Err: err, Description: "failed to write file"})
			}
			responseBytes, err := json.Marshal(doc{Id: request.Id})
			if err != nil {
				panic(appError{Err: err, Description: "failed to marhsall response"})
			}
			_, err = w.Write(responseBytes)
			if err != nil {
				log.Println("Failed to write reponse", err)
			}
		} else {
			panic(appError{Description: "unsupported method", Status: http.StatusBadRequest})
		}
	})
}

func main() {
	var err error
	if docs, err = atylar.New(dataDirectory); err != nil {
		log.Fatal("Couldn't init storage.")
	}

	// http.Handle("/", handler(http.RedirectHandler("/app", http.StatusTemporaryRedirect)))
	http.Handle("/docs", handler(serveDocs()))

	log.Fatal(http.ListenAndServe(":8000", nil))
}
