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
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5"
)

type Org struct {
	Name  string
	Repos []Repo
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

func homeHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		entries, err := os.ReadDir(reposDir)
		if err != nil {
			log.Fatal(err)
		}
		var orgs []Org
		for _, v := range entries {
			if v.IsDir() {
				subEntries, err := os.ReadDir(filepath.Join(reposDir, v.Name()))
				if err != nil {
					log.Fatal(err)
				}

				var repos []Repo
				for _, vsub := range subEntries {
					if vsub.IsDir() && strings.HasSuffix(vsub.Name(), ".git") {
						repos = append(repos, Repo{Name: strings.TrimSuffix(vsub.Name(), ".git")})
					}
				}
				orgs = append(orgs, Org{Name: v.Name(), Repos: repos})
			}
		}
		c.HTML(http.StatusOK, "index.htm", gin.H{
			"Orgs": orgs,
		})
	}
}

func noRouteHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		orgName := c.Param("orgName")
		repoName := c.Param("repoName")
		repo, err := git.PlainOpen(filepath.Join(reposDir, orgName, repoName+".git"))
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

		if len(entries) > 0 {
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].IsDir == entries[j].IsDir {
					return entries[i].Name <= entries[j].Name
				}
				return entries[i].IsDir
			})
		}

		c.HTML(http.StatusOK, "repo.htm", gin.H{
			"OrgName":  orgName,
			"RepoName": repoName,
			"Entries":  entries,
		})
	}
}

var (
	port           int
	host, reposDir string
)

func main() {

	flag.IntVar(&port, "port", 8000, "the port that server listen on")
	flag.StringVar(&host, "host", "0.0.0.0", "the host that server listen on")
	flag.StringVar(&reposDir, "repos", "/srv", "the director where repos store")
	flag.Parse()

	router := gin.Default()
	router.SetHTMLTemplate(newTemplate())

	router.GET("/favicon.ico", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusNotFound)
	})
	router.GET("/", homeHandler())
	router.GET("/:orgName/:repoName", noRouteHandler())

	router.SetTrustedProxies(nil)
	router.Run(fmt.Sprintf("%s:%d", host, port))
}
