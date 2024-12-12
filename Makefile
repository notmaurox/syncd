# Call build - will run prereq steps
.DEFAULT_GOAL := build

# Prevents names from using a file with matching name as target
.PHONY: fmt vet build

# STEP_NAME: PREREQ_STEP_NAME
fmt:
	go fmt ./app/...

vet: fmt
	go fmt ./app/...

build: vet
	go build -o syncd ./app 