# gitcode
self-hosted git server written in Go

## Run gitcode

### server

```bash
# Listen on 127.0.0.1:8787
$ gitcode -port 8787 -repos /home/git
```

### client

```bash
# Access gitcode server through SSH tunnel
$ ssh -N -L 8787:127.0.0.1:8787 git@huoyijie.cn
```

## TODO

* show binary files `done`
* show submodules
* show branchs
* show breadcrumb `done`
* show commits
* ssh proxy/ssh clone
* render markdown/readme `done`
* create new repo
* search repo

## Future

* sign in
* search code
* issues
* pull request
* code diff
* merge request
* hook and build docker image
* publish with k8s