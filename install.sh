#!/bin/bash

set -ex

go mod tidy && \
go build && \
sudo install ./newseum /usr/local/bin/newseum
sudo install ./newseum.desktop /usr/share/applications/newseum.desktop
