#   Copyright 2021 Google LLC
#
#   Licensed under the Apache License, Version 2.0 (the "License");
#   you may not use this file except in compliance with the License.
#   You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
#   Unless required by applicable law or agreed to in writing, software
#   distributed under the License is distributed on an "AS IS" BASIS,
#   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#   See the License for the specific language governing permissions and
#   limitations under the License.
#
PROJECT := $(if $(GOOGLE_PROJECT),$(GOOGLE_PROJECT),$(PROJECT))
IMAGE := $(if $(IMAGE),$(IMAGE),long-cloud-run)
BUILDER := $(if $(BUILDER),$(BUILDER),docker)
REGISTRY := $(if $(REGISTRY),$(REGISTRY),eu.gcr.io)
VERSION := $(if $(VERSION),$(VERSION),v1)
REGION := $(if $(REGION),$(REGION),europe-west4)
FUNCTION_NAME := $(if $(FUNCTION_NAME),$(FUNCTION_NAME),long-cloud-run)
DOCKERFILE := $(if $(DOCKERFILE),$(DOCKERFILE),Dockerfile)
SA := $(if $(SA),$(SA),)
DEPLOYARGS := $(if $(DEPLOYARGS),$(DEPLOYARGS),)

.PHONY: check-env build push run deploy deploy-gcs2bq

all: push

build: check-env
	$(BUILDER) build -t $(REGISTRY)/$(PROJECT)/$(IMAGE):$(VERSION) -f $(DOCKERFILE) .

push: check-env build
	$(BUILDER) push $(REGISTRY)/$(PROJECT)/$(IMAGE):$(VERSION)

run: check-env build
	$(BUILDER) run -it --rm -p 8080:8080 $(REGISTRY)/$(PROJECT)/$(IMAGE):$(VERSION) $(ARGS)

deploy: push
	gcloud run deploy $(FUNCTION_NAME) --image=$(REGISTRY)/$(PROJECT)/$(IMAGE):$(VERSION) \
	  --timeout=60m --concurrency=1 --no-allow-unauthenticated $(DEPLOYARGS) --region=$(REGION)
	$(eval URL=$(shell gcloud run services describe $(FUNCTION_NAME) --region=$(REGION) --format="value(status.address.url)"))
	@echo "To invoke the function, run:\ncurl -H \"Authorization: Bearer \$$(gcloud auth print-identity-token)\" $(URL)" ;

check-env:
ifndef PROJECT
	$(error $$PROJECT environment variable is not set)
endif