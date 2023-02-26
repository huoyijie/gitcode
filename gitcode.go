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
	"github.com/go-git/go-git/v5/plumbing/object"
)

type Org struct {
	Name  string
	Repos []Repo
}

type Repo struct {
	Name string
}

type Entry struct {
	Name, Path string
	IsDir      bool
}

type Dir struct {
	Breadcrumb []string
	Entries    []Entry
}

type File struct {
	Breadcrumb []string
	MIME       string
	Size       int64
	Contents   string
}

func (entry *Entry) IsParent() bool {
	return entry.IsDir && entry.Name == ".."
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

func parseParams(path string) (orgName, repoName, branchName string, Breadcrumb []string) {
	tmp := strings.Split(strings.TrimSuffix(path, "/"), "/")
	orgName = tmp[1]
	repoName = tmp[2]
	branchName = tmp[4]
	Breadcrumb = tmp[5:]
	return
}

func getRepoTree(orgName, repoName, branchName string) *object.Tree {
	repo, err := git.PlainOpen(filepath.Join(reposDir, orgName, repoName+".git"))
	if err != nil {
		log.Fatal(err)
	}

	// todo
	// get branch by `branchName`
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

	return tree
}

func getEntryType(isFile bool) string {
	if isFile {
		return "blob"
	} else {
		return "tree"
	}
}

func getTreeEntries(tree *object.Tree, orgName, repoName, branchName, entryPath string) []Entry {
	var entries []Entry

	pathFmt := "/" + filepath.Join(orgName, repoName, "%s", branchName, entryPath, "%s")

	dstTree := tree
	if len(entryPath) > 0 {
		var err error
		if dstTree, err = tree.Tree(entryPath); err != nil {
			log.Fatal(err)
		}
		entries = append(entries, Entry{
			Name:  "..",
			Path:  fmt.Sprintf(pathFmt, getEntryType(false), ".."),
			IsDir: true,
		})
	}

	for _, entry := range dstTree.Entries {
		entries = append(entries, Entry{
			Name:  entry.Name,
			Path:  fmt.Sprintf(pathFmt, getEntryType(entry.Mode.IsFile()), entry.Name),
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
	return entries
}

func noRouteHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		// /:orgName/:repoName/tree/:branchName/...
		if strings.Contains(path, "/tree/") {
			orgName, repoName, branchName, breadcrumb := parseParams(path)

			tree := getRepoTree(orgName, repoName, branchName)

			entries := getTreeEntries(tree, orgName, repoName, branchName, strings.Join(breadcrumb, "/"))

			c.HTML(http.StatusOK, "repo.htm", gin.H{
				"OrgName":    orgName,
				"RepoName":   repoName,
				"BranchName": branchName,
				"Tree":       true,
				"Dir":        Dir{breadcrumb, entries},
			})
		} else if strings.Contains(path, "/blob/") {
			// /:orgName/:repoName/blob/:branchName/...
			orgName, repoName, branchName, breadcrumb := parseParams(path)

			tree := getRepoTree(orgName, repoName, branchName)

			file, err := tree.File(strings.Join(breadcrumb, "/"))
			if err != nil {
				log.Fatal(err)
			}

			contents := "Binary"
			if bin, err := file.IsBinary(); err != nil {
				log.Fatal(err)
			} else if !bin {
				var err error
				contents, err = file.Contents()
				if err != nil {
					log.Fatal(err)
				}
			}

			c.HTML(http.StatusOK, "repo.htm", gin.H{
				"OrgName":    orgName,
				"RepoName":   repoName,
				"BranchName": branchName,
				"Blob":       true,
				"File":       File{breadcrumb, "MIME", file.Size, contents},
			})
		} else {
			c.AbortWithStatus(http.StatusNotFound)
		}
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

	router.GET("/", homeHandler())
	router.NoRoute(noRouteHandler())

	router.SetTrustedProxies(nil)
	router.Run(fmt.Sprintf("%s:%d", host, port))
}
