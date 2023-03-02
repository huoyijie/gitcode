package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/casbin/casbin"
	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/xeonx/timeago"
	yaml "gopkg.in/yaml.v2"
)

type Action string

const (
	READ  Action = "read"
	WRITE Action = "write"
)

func (a Action) Val() string {
	return string(a)
}

type Code int

const (
	ErrInvalidUsernameOrPassword Code = -(iota + 10000)
	ErrRepositoryAlreadyExists
)

type GitcodeConfig struct {
	Secret, Username, Password string
	Ignore                     []string
}

func loadConfig(conf string) (config *GitcodeConfig) {
	data, err := os.ReadFile(conf)
	if err != nil {
		log.Fatal(err)
	}

	config = &GitcodeConfig{}
	err = yaml.Unmarshal(data, config)
	if err != nil {
		log.Fatal(err)
	}
	return
}

type SigninForm struct {
	Username string `json:"username" binding:"required,alphanum,min=6,max=32"`
	Password string `json:"password" binding:"required,alphanum,len=64"`
}

type Org struct {
	Name  string
	Repos []Repo
}

type Repo struct {
	Name, DefaultBranch string
}

func (r *Repo) Empty() bool {
	return r.DefaultBranch == ""
}

type Entry struct {
	Name, Path                    string
	IsDir, IsSubmodule, IsSymlink bool
	Commit                        Commit
}

func (entry *Entry) IsParent() bool {
	return entry.IsDir && entry.Name == ".."
}

func (entry *Entry) IsFolder() bool {
	return entry.IsDir || entry.IsSubmodule
}

type Dir struct {
	Entries []Entry
}

type File struct {
	Size    int64
	RawPath string
	Lang    string
}

type BreadcrumbItem struct {
	Name, Path string
	Last       bool
}

type Commit struct {
	Author, Email, Message, TimeAgo string
}

//go:embed templates/*
var tmplFS embed.FS

func newTemplate() (tmpl *template.Template) {
	tmpl = template.Must(template.New("").ParseFS(tmplFS, "templates/*.htm"))
	return
}

func loadOrgs(username string) (orgs []Org, defaultOrg string) {
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		log.Fatal(err)
	}
Loop:
	for _, v := range entries {
		hidden := strings.HasPrefix(v.Name(), ".")
		// ignore entries that are hidden or aren't directories.
		if hidden || !v.IsDir() {
			continue
		}

		// ignore entries that are ignored
		for _, ignore := range gitcodeCfg.Ignore {
			if v.Name() == ignore {
				continue Loop
			}
		}

		// check permissions
		if ok := enforcer.Enforce(username, v.Name(), READ.Val()); !ok {
			continue
		}

		subEntries, err := os.ReadDir(filepath.Join(reposDir, v.Name()))
		if err != nil {
			log.Fatal(err)
		}

		var repos []Repo
		for _, vsub := range subEntries {
			if vsub.IsDir() && strings.HasSuffix(vsub.Name(), ".git") {
				repo, err := git.PlainOpen(filepath.Join(reposDir, v.Name(), vsub.Name()))
				if err != nil {
					log.Fatal(err)
				}

				defaultBranch := ""
				head, err := repo.Head()
				// New repository dosen't have any refs at all.
				if err == nil {
					defaultBranch = head.Name().Short()
				}

				repos = append(repos, Repo{
					Name:          strings.TrimSuffix(vsub.Name(), ".git"),
					DefaultBranch: defaultBranch,
				})
			}
		}
		orgs = append(orgs, Org{Name: v.Name(), Repos: repos})
	}

	if len(orgs) > 0 {
		defaultOrg = orgs[0].Name
	} else {
		defaultOrg = ""
	}
	return
}

func authHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		if token, err := c.Cookie("token"); err == nil {
			if username, expired, err := ParseToken(token); err == nil && !expired {
				c.Set("username", username)
				c.Next()
				return
			}
		}
		c.Set("username", "guest")
		c.Next()
	}
}

func homeHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		orgs, defaultOrg := loadOrgs(c.GetString("username"))
		c.HTML(http.StatusOK, "index.htm", gin.H{
			"Year": time.Now().Year(),
			"Hostname":   hostname,
			"Orgs":       orgs,
			"DefaultOrg": defaultOrg,
			"Username":   c.GetString("username"),
		})
	}
}

func signinPageHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		c.HTML(http.StatusOK, "signin.htm", gin.H{})
	}
}

func signinHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		form := &SigninForm{}
		if err := c.BindJSON(form); err != nil {
			c.AbortWithError(http.StatusBadRequest, err)
			return
		}
		fmt.Println(form)
		if form.Username != gitcodeCfg.Username || form.Password != gitcodeCfg.Password {
			c.JSON(http.StatusOK, gin.H{
				"Code": ErrInvalidUsernameOrPassword,
			})
			return
		}
		token, err := GenerateToken(form.Username)
		if err != nil {
			log.Fatal(err)
		}
		c.SetCookie("token", token, COOKIE_MAX_TTL, "/", hostname, true, true)
		c.JSON(http.StatusOK, gin.H{
			"Code":  0,
			"Token": token,
		})
	}
}

func signoutHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		c.SetCookie("token", "", -1, "/", hostname, true, true)
		c.JSON(http.StatusOK, gin.H{"Code": 0})
	}
}

func newRepoHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		orgName := c.Param("orgName")
		repoName := c.Param("repoName")
		repoPath := filepath.Join(reposDir, orgName, repoName+".git")

		// check permissions
		if ok := enforcer.Enforce(c.GetString("username"), orgName, WRITE.Val()); !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		var code Code
		if _, err := git.PlainInit(repoPath, true); err != nil {
			// error occurs
			if !errors.Is(err, git.ErrRepositoryAlreadyExists) {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
			code = ErrRepositoryAlreadyExists
		}

		c.JSON(http.StatusOK, gin.H{
			"Code": code,
		})
	}
}

func parseParams(path string) (orgName, repoName, branchName string, Breadcrumb []string) {
	tmp := strings.Split(path, "/")
	orgName = tmp[1]
	repoName = tmp[2]
	branchName = tmp[4]
	Breadcrumb = tmp[5:]
	return
}

func getRepoTree(orgName, repoName, branchName string) (*git.Repository, *object.Tree, plumbing.Hash) {
	repo, err := git.PlainOpen(filepath.Join(reposDir, orgName, repoName+".git"))
	if err != nil {
		log.Fatal(err)
	}

	branches, err := repo.Branches()
	if err != nil {
		log.Fatal(err)
	}
	defer branches.Close()

	var commitHash plumbing.Hash
	branch, err := branches.Next()
	for ; err == nil; branch, err = branches.Next() {
		if branch.Name().Short() == branchName {
			commitHash = branch.Hash()
			break
		}
	}
	if err != nil {
		commitHash = plumbing.NewHash(branchName)
	}

	commit, err := repo.CommitObject(commitHash)
	if err != nil {
		log.Fatal(err)
	}

	tree, err := commit.Tree()
	if err != nil {
		log.Fatal(err)
	}

	return repo, tree, commitHash
}

func getEntryType(isFile bool) string {
	if isFile {
		return "blob"
	} else {
		return "tree"
	}
}

func isSymlink(mode filemode.FileMode) bool {
	return mode == filemode.Symlink
}

func isSubmodule(mode filemode.FileMode) bool {
	return mode == filemode.Submodule
}

func isDir(mode filemode.FileMode) bool {
	return mode == filemode.Dir
}

func getEntryPath(cfg *config.Config, entry object.TreeEntry, pathFmt string) string {
	if isSubmodule(entry.Mode) {
		sub := cfg.Submodules[entry.Name]
		repoPath := strings.Split(sub.URL, ":")[1]
		repoPath = strings.TrimSuffix(repoPath, ".git")

		if strings.HasPrefix(repoPath, "/") {
			// absolute path
			basePath := strings.TrimSuffix(reposDir, "/")
			repoPath = strings.TrimPrefix(repoPath, basePath)
		} else {
			// relative path
			repoPath = "/" + repoPath
		}

		return fmt.Sprintf("%s/tree/%s", repoPath, entry.Hash)
	}
	return fmt.Sprintf(pathFmt, getEntryType(entry.Mode.IsFile()), entry.Name)
}

func getLogOptions(commitHash plumbing.Hash, entry string) (opts *git.LogOptions) {
	opts = &git.LogOptions{
		From:  commitHash,
		Order: git.LogOrderDFSPost,
		PathFilter: func(path string) bool {
			return strings.HasPrefix(path, entry)
		},
	}
	return
}

func getTreeEntries(repo *git.Repository, tree *object.Tree, commitHash plumbing.Hash, orgName, repoName, branchName, entryPath string) ([]Entry, bool) {
	var (
		entries    []Entry
		loadReadme bool
		cfg        *config.Config
	)

	if submodules, err := tree.File(".gitmodules"); err == nil {
		reader, err := submodules.Reader()
		if err != nil {
			log.Fatal(err)
		}
		defer reader.Close()

		cfg, err = config.ReadConfig(reader)
		if err != nil {
			log.Fatal(err)
		}
	}

	pathFmt := "/" + filepath.Join(orgName, repoName, "%s", branchName, entryPath, "%s")

	dstTree := tree
	if len(entryPath) > 0 {
		var err error
		if dstTree, err = tree.Tree(entryPath); err != nil {
			log.Fatal(err)
		}
		entries = append(entries, Entry{
			Name:        "..",
			Path:        fmt.Sprintf(pathFmt, getEntryType(false), ".."),
			IsDir:       true,
			IsSubmodule: false,
			IsSymlink:   false,
		})
	}

	var wg sync.WaitGroup
	var lock sync.Mutex
	now := time.Now()
	for i := range dstTree.Entries {
		wg.Add(1)

		i := i
		go func() {
			defer wg.Done()

			entry := dstTree.Entries[i]
			commits, err := repo.Log(getLogOptions(commitHash, filepath.Join(entryPath, entry.Name)))
			if err != nil {
				log.Fatal(err)
			}
			defer commits.Close()
			commit, err := commits.Next()
			if err != nil {
				log.Fatal(err)
			}

			lock.Lock()
			defer lock.Unlock()
			entries = append(entries, Entry{
				Name:        entry.Name,
				Path:        getEntryPath(cfg, entry, pathFmt),
				IsDir:       isDir(entry.Mode),
				IsSubmodule: isSubmodule(entry.Mode),
				IsSymlink:   isSymlink(entry.Mode),
				Commit: Commit{
					Author:  commit.Author.Name,
					Email:   commit.Author.Email,
					Message: commit.Message,
					TimeAgo: timeago.English.Format(commit.Author.When),
				},
			})
			if entry.Mode.IsFile() && entry.Name == "README.md" {
				loadReadme = true
			}
		}()
	}
	wg.Wait()
	fmt.Println("cost", time.Since(now))

	if len(entries) > 0 {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsFolder() == entries[j].IsFolder() {
				return entries[i].Name <= entries[j].Name
			}
			return entries[i].IsFolder()
		})
	}
	return entries, loadReadme
}

func getBreadcrumb(branchPath string, breadcrumb []string) []BreadcrumbItem {
	tmp := make([]BreadcrumbItem, len(breadcrumb))
	for i := range breadcrumb {
		tmp[i].Name = breadcrumb[i]
		tmp[i].Path = filepath.Join(branchPath, strings.Join(breadcrumb[:i+1], "/"))
		tmp[i].Last = i == len(breadcrumb)-1
	}
	return tmp
}

func noRouteHandler() func(*gin.Context) {
	return func(c *gin.Context) {
		path := strings.TrimSuffix(c.Request.URL.Path, "/")
		isTree := strings.Contains(path, "/tree/")
		isBlob := strings.Contains(path, "/blob/")

		// only handle tree or blob requests
		if !(isTree || isBlob) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		orgName, repoName, branchName, breadcrumb := parseParams(path)
		// ignore entries that are ignored
		for _, ignore := range gitcodeCfg.Ignore {
			if orgName == ignore {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
		}

		// check permissions
		if ok := enforcer.Enforce(c.GetString("username"), orgName, READ.Val()); !ok {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		branchPath := fmt.Sprintf("/%s/%s/tree/%s", orgName, repoName, branchName)
		entryPath := strings.Join(breadcrumb, "/")

		repo, tree, commitHash := getRepoTree(orgName, repoName, branchName)

		orgs, defaultOrg := loadOrgs(c.GetString("username"))
		// /:orgName/:repoName/tree/:branchName/...
		if isTree {
			entries, loadReadme := getTreeEntries(repo, tree, commitHash, orgName, repoName, branchName, entryPath)

			c.HTML(http.StatusOK, "repo.htm", gin.H{
				"Year": time.Now().Year(),
				"Hostname":   hostname,
				"OrgName":    orgName,
				"RepoName":   repoName,
				"BranchName": branchName,
				"BranchPath": branchPath,
				"Tree":       true,
				"Root":       len(breadcrumb) == 0,
				"GitClone":   fmt.Sprintf("git clone git@%s:%s/%s.git", hostname, orgName, repoName),
				"Breadcrumb": getBreadcrumb(branchPath, breadcrumb),
				"Dir":        Dir{entries},
				"LoadReadme": loadReadme,
				"ReadmePath": filepath.Join(fmt.Sprintf("/%s/%s/blob/%s", orgName, repoName, branchName), entryPath, "README.md"),
				"Orgs":       orgs,
				"DefaultOrg": defaultOrg,
				"Username":   c.GetString("username"),
			})
			return
		}

		// /:orgName/:repoName/blob/:branchName/...[?raw=true]
		if isBlob {
			file, err := tree.File(entryPath)
			if err != nil {
				log.Fatal(err)
			}
			isBin, err := file.IsBinary()
			if err != nil {
				log.Fatal(err)
			}

			ext := filepath.Ext(path)
			raw := c.Query("raw") == "true"
			if raw || isBin {
				reader, err := file.Reader()
				if err != nil {
					log.Fatal(err)
				}
				defer reader.Close()

				contentType := mime.TypeByExtension(ext)
				if len(contentType) == 0 {
					contentType = "text/plain; charset=utf-8"
				}
				c.Writer.Header().Set("Content-type", contentType)
				c.Status(200)
				io.Copy(c.Writer, reader)
			} else {
				lang := "none"
				if len(ext) > 0 {
					lang = ext[1:]
				}
				if lang == "md" {
					c.HTML(http.StatusOK, "readme.htm", gin.H{
						"Year": time.Now().Year(),
						"Hostname":   hostname,
						"BasePath":   filepath.Dir(path),
						"HomePage":   filepath.Base(path) + "?raw=true",
						"OrgName":    orgName,
						"RepoName":   repoName,
						"Orgs":       orgs,
						"DefaultOrg": defaultOrg,
						"Username":   c.GetString("username"),
					})
				} else {
					c.HTML(http.StatusOK, "repo.htm", gin.H{
						"Year": time.Now().Year(),
						"Hostname":   hostname,
						"OrgName":    orgName,
						"RepoName":   repoName,
						"BranchName": branchName,
						"BranchPath": branchPath,
						"Blob":       true,
						"Breadcrumb": getBreadcrumb(branchPath, breadcrumb),
						"File":       File{file.Size, path + "?raw=true", lang},
						"Orgs":       orgs,
						"DefaultOrg": defaultOrg,
						"Username":   c.GetString("username"),
					})
				}
			}
		}
	}
}

var (
	port                     int
	host, hostname, reposDir string
	gitcodeCfg               *GitcodeConfig
	enforcer                 *casbin.Enforcer
)

func main() {

	flag.IntVar(&port, "port", 8000, "the port that server listen on")
	flag.StringVar(&host, "host", "127.0.0.1", "the host that server listen on")
	flag.StringVar(&hostname, "hostname", "huoyijie.cn", "the host name of the server")
	flag.StringVar(&reposDir, "repos", "/srv", "the directory where repos store")
	flag.Parse()
	gitcodeCfg = loadConfig(filepath.Join(reposDir, "gitcode.yaml"))

	enforcer = casbin.NewEnforcer(filepath.Join(reposDir, "rbac_model.conf"), filepath.Join(reposDir, "rbac_policy.csv"))

	router := gin.Default()
	router.SetHTMLTemplate(newTemplate())
	router.GET("signin", signinPageHandler())
	router.POST("signin", signinHandler())
	router.GET("signout", signoutHandler())
	router.GET("/", authHandler(), homeHandler())
	router.POST("orgs/:orgName/repos/:repoName/new", authHandler(), newRepoHandler())
	router.NoRoute(authHandler(), noRouteHandler())

	router.SetTrustedProxies(nil)
	router.Run(fmt.Sprintf("%s:%d", host, port))
}
