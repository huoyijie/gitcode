# gitcode
self-hosted git server written in Go

## Run gitcode

### server

Run gitcode manually
```bash
# Listen on 127.0.0.1:8787
$ gitcode -port 8787 -repos /home/git
```

Run gitcode as a service

Create a systemd config file `/etc/systemd/system/gitcode.service`
```conf
[Unit]
Description=Gitcode

[Service]
User=git
Group=git
Type=idle
Environment="GIN_MODE=release"
ExecStart=/home/ubuntu/go/bin/gitcode -port 8787 -repos /home/git
WorkingDirectory=/home/ubuntu/gowork/gitcode
Restart=always
KillMode=process

[Install]
WantedBy=multi-user.target
```

Start gitcode service
```bash
$ sudo systemctl daemon-reload
$ sudo systemctl enable --now gitcode
```

View logs
```bash
$ systemctl -fu gitcode
```

### client

Access gitcode server through SSH tunnel
```bash
$ ssh -N -L 8787:127.0.0.1:8787 -o ServerAliveInterval=5 git@huoyijie.cn
```

Open `http://127.0.0.1:8787/` with the browser

## TODO

* show binary files `done`
* show submodules
* show branchs
* show breadcrumb `done`
* show commits
* ssh proxy/ssh clone `done`
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