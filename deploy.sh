#!/bin/bash

# Check if an SSH host is provided
if [ "$#" -ne 2 ]; then
    echo "Usage: $0 ssh_host deploy_path"
    exit 1
fi

# Define the SSH host
SSH_HOST=$1
DEPLOY_PATH=$2

env CGO_LDFLAGS="-Llib -lmp3lame -lopus -logg" GOOS=linux GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-linux-gnu-gcc go build
ssh tuna2024 "sudo service gptbot stop" > /dev/null 2>&1
scp chatgpt-bot $SSH_HOST:$DEPLOY_PATH
ssh $SSH_HOST "sudo service gptbot start" > /dev/null 2>&1
