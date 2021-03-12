package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var pdfServerHOST = os.Getenv("REMOTE_PDF_SERVER")

type fillformRequest struct {
	Form            Form   `json:"form"`
	Filename        string `json:"filename"`
	CheckedString   string `json:"checkedString"`
	UncheckedString string `json:"uncheckedString"`
}

func handleFillform(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "post requests only", http.StatusMethodNotAllowed)
		return
	}

	ffReq := fillformRequest{}
	if err := json.NewDecoder(r.Body).Decode(&ffReq); err != nil {
		log.Println(err)
		http.Error(w, "invalid input json", http.StatusBadRequest)
		return
	}

	// create temp pdf
	tmpDir, err := ioutil.TempDir("/tmp", "fillform-")
	if err != nil {
		log.Println(err)
		http.Error(w, "could not create temp dir", http.StatusInternalServerError)
		return
	}

	// Remove the temporary directory on defer again.
	defer func() {
		os.RemoveAll(tmpDir)
	}()

	var templatePath string

	if pdfServerHOST == "" {
		// asumming that the pdf file is available locally.
		var err error
		templatePath, err = filepath.Abs(ffReq.Filename)
		if err != nil {
			log.Println(err)
			http.Error(w, "could not set abs template path", http.StatusInternalServerError)
			return
		}
	} else {

		// Get pdf file from tdocServePDF service.
		//  write the file to the tmp folder.
		response, err := http.Get(fmt.Sprintf("%s/%s", pdfServerHOST, ffReq.Filename))
		if err != nil {
			log.Println(err)
			http.Error(w, "document blob get request failed", http.StatusInternalServerError)
			return
		}
		defer response.Body.Close()

		if response.StatusCode == http.StatusOK {
			data := struct {
				Document string `json:"document"`
			}{}
			if err := json.NewDecoder(response.Body).Decode(&data); err != nil {
				log.Println(err)
				http.Error(w, fmt.Sprintf("failed to parse json (%v)", err), http.StatusInternalServerError)
				return
			}
			documentData, err := base64.StdEncoding.DecodeString(data.Document)
			if err != nil {
				log.Println(err)
				http.Error(w, "failed to b64 decode response body", http.StatusInternalServerError)
				return
			}
			fd, err := os.Create(filepath.Join(tmpDir, ffReq.Filename))
			if err != nil {
				log.Println(err)
				http.Error(w, "couldn't create file", http.StatusInternalServerError)
				return
			}
			defer fd.Close()
			if _, err := fd.Write(documentData); err != nil {
				log.Println(err)
				http.Error(w, "couldn't write pdf file", http.StatusInternalServerError)
				return
			}

			if err := fd.Sync(); err != nil {
				log.Println(err)
				http.Error(w, "couldn't sync pdf file", http.StatusInternalServerError)
				return
			}

			templatePath = filepath.Join(tmpDir, ffReq.Filename)

		} else {
			http.Error(w, "couldn't get document from the Pdf Server", http.StatusInternalServerError)
			return
		}
	}

	var f *os.File
	if ffReq.Form != nil {
		fdfFile := filepath.Clean(tmpDir + "/data.fdf")
		if err := createFdfFile(ffReq.Form, fdfFile, ffReq.CheckedString, ffReq.UncheckedString); err != nil {
			log.Println(err)
			http.Error(w, "could not create fdf file", http.StatusInternalServerError)
			return
		}

		outputFile := filepath.Clean(tmpDir + "/output.pdf")

		// Create the pdftk command line arguments.
		args := []string{
			templatePath,
			"fill_form", fdfFile,
			"output", outputFile,
		}

		if err := runCommandInPath(tmpDir, "pdftk", args...); err != nil {
			log.Println(err)
			http.Error(w, "pdftk reported an error", http.StatusInternalServerError)
			return
		}

		f, err = os.Open(outputFile)
		if err != nil {
			log.Println(err)
			http.Error(w, "could not open output file", http.StatusInternalServerError)
			return
		}
	} else {
		documentPath, err := filepath.Abs(templatePath)
		if err != nil {
			log.Println(err)
			http.Error(w, "could not set abs template path", http.StatusInternalServerError)
			return
		}
		f, err = os.Open(documentPath)
		if err != nil {
			log.Println(err)
			http.Error(w, "could not open output file", http.StatusInternalServerError)
			return
		}
	}

	http.ServeContent(w, r, "out.pdf", time.Now(), f)
}

type multistampRequest struct {
	SignaturePDF string `json:"signaturePDF"`
	FormPDF      string `json:"formPDF"`
}

func handleMultistamp(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "post requests only", http.StatusMethodNotAllowed)
		return
	}

	msReq := multistampRequest{}
	if err := json.NewDecoder(r.Body).Decode(&msReq); err != nil {
		log.Println(err)
		http.Error(w, "invalid input json", http.StatusBadRequest)
		return
	}

	tmpDir, err := ioutil.TempDir("/tmp", "multistamp-")
	if err != nil {
		log.Println(err)
		http.Error(w, "could not create temp dir", http.StatusInternalServerError)
		return
	}

	// Remove the temporary directory on defer again.
	defer func() {
		os.RemoveAll(tmpDir)
	}()

	sigPDFdecoded, err := base64.StdEncoding.DecodeString(msReq.SignaturePDF)
	if err != nil {
		log.Println(err)
		http.Error(w, "could not decode signature file", http.StatusInternalServerError)
		return
	}

	sigPath := tmpDir + "/sig.pdf"
	if err := ioutil.WriteFile(sigPath, sigPDFdecoded, 0600); err != nil {
		log.Println(err)
		http.Error(w, "could not write signature file", http.StatusInternalServerError)
		return
	}

	formPDFdecoded, err := base64.StdEncoding.DecodeString(msReq.FormPDF)
	if err != nil {
		log.Println(err)
		http.Error(w, "could not decode form file", http.StatusInternalServerError)
		return
	}

	formPath := tmpDir + "/form.pdf"
	if err := ioutil.WriteFile(formPath, formPDFdecoded, 0600); err != nil {
		log.Println(err)
		http.Error(w, "could not write form file", http.StatusInternalServerError)
		return
	}

	outputFile := filepath.Clean(tmpDir + "/output.pdf")

	// Create the pdftk command line arguments.
	args := []string{
		formPath,
		"multistamp", sigPath,
		"output", outputFile,
	}

	if err := runCommandInPath(tmpDir, "pdftk", args...); err != nil {
		log.Println(err)
		http.Error(w, "pdftk reported an error", http.StatusInternalServerError)
		return
	}

	f, err := os.Open(outputFile)
	if err != nil {
		log.Println(err)
		http.Error(w, "could not open output file", http.StatusInternalServerError)
		return
	}

	http.ServeContent(w, r, "out.pdf", time.Now(), f)
}

// runCommandInPath runs a command and waits for it to exit.
// The working directory is also set.
// The stderr error message is returned on error.
func runCommandInPath(dir, name string, args ...string) error {
	// Create the command.
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	cmd.Dir = dir

	// Start the command and wait for it to exit.
	if err := cmd.Run(); err != nil {
		return fmt.Errorf(strings.TrimSpace(stderr.String()))
	}

	return nil
}

var version = "dev" // set during build with ldflags

func main() {
	http.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	http.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(version)) })

	http.HandleFunc("/fillform", handleFillform)
	http.HandleFunc("/multistamp", handleMultistamp)

	log.Fatal(http.ListenAndServe(":8082", nil))
}
