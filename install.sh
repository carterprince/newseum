#!/bin/bash -ex

PREFIX="/usr/local"

go mod tidy
go build -ldflags "-s -w" -o newseum

[ -d ${PREFIX}/bin ] || sudo mkdir -p ${PREFIX}/bin
sudo install ./newseum ${PREFIX}/bin/newseum
[ -d ${PREFIX}/share/applications ] || sudo mkdir -p ${PREFIX}/share/applications
sudo install -m 644 ./newseum.desktop ${PREFIX}/share/applications/newseum.desktop
