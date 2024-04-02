#!/bin/bash

# check if number is greater or equal to 2
if [ "$#" -lt 2 ]; then
    echo "Usage: $0 ssh_host deploy_path [arm|x86]"
    exit 1
fi

# Define the SSH host
SSH_HOST=$1
DEPLOY_PATH=$2
ARCH="x86"

if [ "$#" -eq 3 ]; then
  ARCH=$3
fi
if [ $ARCH == "arm" ]; then
    echo "Building for ARM"
    env CGO_LDFLAGS="-Llib_arm -lmp3lame -lopus -logg" GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=aarch64-unknown-linux-gnu-gcc go build
else
    echo "Building for x86"
    env CGO_LDFLAGS="-Llib_x86 -lmp3lame -lopus -logg" GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build
fi

#env CGO_LDFLAGS="-Llib -lmp3lame -lopus -logg" GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build
ssh $SSH_HOST "sudo service gptbot stop" > /dev/null 2>&1
scp chatgpt-bot $SSH_HOST:$DEPLOY_PATH
ssh $SSH_HOST "sudo service gptbot start" > /dev/null 2>&1
