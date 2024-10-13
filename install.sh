#!/bin/bash

set -ex

go mod tidy && \
go build && \
sudo install ./newseum /usr/local/bin/newseum
