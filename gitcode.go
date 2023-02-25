package main

import (
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5"
)

type Org struct {
	Name string
}

type Repo struct {
	Name string
}

type DirEntry struct {
	Name  string
	IsDir bool
}

//go:embed templates/*
var tmplFS embed.FS

func newTemplate() (tmpl *template.Template) {
	tmpl = template.Must(template.New("").ParseFS(tmplFS, "templates/*.htm"))
	return
}

func main() {
	var (
		port            int
		host, REPOS_DIR string
	)

	flag.IntVar(&port, "port", 8000, "the port that server listen on")
	flag.StringVar(&host, "host", "0.0.0.0", "the host that server listen on")
	flag.StringVar(&REPOS_DIR, "repos", "/srv", "the director where repos store")
	flag.Parse()

	router := gin.Default()
	router.SetHTMLTemplate(newTemplate())

	router.GET("/favicon.ico", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusNotFound)
	})
	router.GET("/", func(c *gin.Context) {
		entries, err := os.ReadDir(REPOS_DIR)
		if err != nil {
			log.Fatal(err)
		}
		var orgs []Org
		for _, v := range entries {
			if v.IsDir() {
				orgs = append(orgs, Org{Name: v.Name()})
			}
		}
		c.HTML(http.StatusOK, "index.htm", gin.H{
			"Orgs": orgs,
		})
	})
	router.GET("/:orgName", func(c *gin.Context) {
		orgName := c.Param("orgName")
		entries, err := os.ReadDir(filepath.Join(REPOS_DIR, orgName))
		if err != nil {
			log.Fatal(err)
		}
		var repos []Repo
		for _, v := range entries {
			if v.IsDir() {
				repos = append(repos, Repo{Name: strings.TrimSuffix(v.Name(), ".git")})
			}
		}

		c.HTML(http.StatusOK, "org.htm", gin.H{
			"OrgName": orgName,
			"Repos":   repos,
		})
	})
	router.GET("/:orgName/:repoName", func(c *gin.Context) {
		orgName := c.Param("orgName")
		repoName := c.Param("repoName")
		repo, err := git.PlainOpen(filepath.Join(REPOS_DIR, orgName, repoName+".git"))
		if err != nil {
			log.Fatal(err)
		}

		head, err := repo.Head()
		if err != nil {
			log.Fatal(err)
		}

		commit, err := repo.CommitObject(head.Hash())
		if err != nil {
			log.Fatal(err)
		}

		tree, err := commit.Tree()
		if err != nil {
			log.Fatal(err)
		}
		var entries []DirEntry
		for _, entry := range tree.Entries {
			entries = append(entries, DirEntry{
				Name:  entry.Name,
				IsDir: !entry.Mode.IsFile(),
			})
		}

		c.HTML(http.StatusOK, "repo.htm", gin.H{
			"OrgName":  orgName,
			"RepoName": repoName,
			"Entries":  entries,
		})
	})

	router.SetTrustedProxies(nil)
	router.Run(fmt.Sprintf("%s:%d", host, port))
}
