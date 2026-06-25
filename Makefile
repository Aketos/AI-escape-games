# Makefile for Scaleway Serverless Deployment (Backend & Frontend)

REGION ?= fr-par
REGISTRY_NAME ?= escape-game-repo
BACKEND_IMAGE ?= escape-game-backend
FRONTEND_IMAGE ?= escape-game-frontend
TAG ?= latest
REGISTRY_ENDPOINT ?= rg.$(REGION).scw.cloud/$(REGISTRY_NAME)

SCW_PROJECT_ID ?= $(shell scw config get default-project-id)
# SCW_SECRET_KEY ?= $(shell scw config get secret-key)
SCW_SECRET_KEY ?= 49ae0a32-c24b-45ef-a050-cc12ed43c368
UNMUTE_WS_URL ?= ws://38.244.145.196:40927/v1/realtime
UNMUTE_VOICE ?= cml-tts/fr/12080_11650_000047-0001_enhanced.wav
LLM_UPSTREAM_URL ?= https://api.scaleway.ai/0cf90930-f182-408a-a3c1-78350db643e4/v1
LLM_MODEL ?= gemma-3-27b-it
LLM_API_KEY ?= $(SCW_SECRET_KEY)

.PHONY: scw-login build-push-backend build-push-frontend deploy-backend deploy-frontend deploy-all init-registry init-namespace

# 1. Authenticate Docker to Scaleway Container Registry
scw-login:
	scw registry login

# Create registry namespace if not exists
init-registry:
	scw registry namespace create name=$(REGISTRY_NAME) region=$(REGION) || true

# 2. Build and push Backend
build-push-backend: init-registry
	docker build -t $(REGISTRY_ENDPOINT)/$(BACKEND_IMAGE):$(TAG) .
	docker push $(REGISTRY_ENDPOINT)/$(BACKEND_IMAGE):$(TAG)

# 3. Build and push Frontend
build-push-frontend: init-registry
	docker build -t $(REGISTRY_ENDPOINT)/$(FRONTEND_IMAGE):$(TAG) ./frontend
	docker push $(REGISTRY_ENDPOINT)/$(FRONTEND_IMAGE):$(TAG)

# Ensure Serverless Namespace exists and is ready
init-namespace:
	@NAMESPACE_ID=$$(scw container namespace list name=$(REGISTRY_NAME)-ns region=$(REGION) -o json | jq -r '.[0].id // empty'); \
	if [ -z "$$NAMESPACE_ID" ]; then \
		echo "Creating Serverless namespace and waiting to be ready..."; \
		scw container namespace create name=$(REGISTRY_NAME)-ns region=$(REGION) -w; \
	fi

# 4. Deploy Backend
deploy-backend: build-push-backend init-namespace
	@echo "Deploying backend container..."
	@NAMESPACE_ID=$$(scw container namespace list name=$(REGISTRY_NAME)-ns region=$(REGION) -o json | jq -r '.[0].id'); \
	CONTAINER_ID=$$(scw container container list namespace-id=$$NAMESPACE_ID name=$(BACKEND_IMAGE)-container region=$(REGION) -o json | jq -r '.[0].id // empty'); \
	if [ -z "$$CONTAINER_ID" ]; then \
		echo "Creating new container..."; \
		CONTAINER_ID=$$(scw container container create namespace-id=$$NAMESPACE_ID name=$(BACKEND_IMAGE)-container registry-image=$(REGISTRY_ENDPOINT)/$(BACKEND_IMAGE):$(TAG) port=8080 environment-variables.SCALEWAY_PROJECT_ID="$(SCW_PROJECT_ID)" environment-variables.UNMUTE_WS_URL="$(UNMUTE_WS_URL)" environment-variables.UNMUTE_VOICE="$(UNMUTE_VOICE)" environment-variables.LLM_UPSTREAM_URL="$(LLM_UPSTREAM_URL)" environment-variables.LLM_MODEL="$(LLM_MODEL)" secret-environment-variables.0.key="SCALEWAY_MOSHI_API_KEY" secret-environment-variables.0.value="$(SCW_SECRET_KEY)" secret-environment-variables.1.key="LLM_API_KEY" secret-environment-variables.1.value="$(LLM_API_KEY)" region=$(REGION) -o json | jq -r '.id'); \
	else \
		echo "Updating existing container..."; \
		scw container container update $$CONTAINER_ID registry-image=$(REGISTRY_ENDPOINT)/$(BACKEND_IMAGE):$(TAG) environment-variables.SCALEWAY_PROJECT_ID="$(SCW_PROJECT_ID)" environment-variables.UNMUTE_WS_URL="$(UNMUTE_WS_URL)" environment-variables.UNMUTE_VOICE="$(UNMUTE_VOICE)" environment-variables.LLM_UPSTREAM_URL="$(LLM_UPSTREAM_URL)" environment-variables.LLM_MODEL="$(LLM_MODEL)" secret-environment-variables.0.key="SCALEWAY_MOSHI_API_KEY" secret-environment-variables.0.value="$(SCW_SECRET_KEY)" secret-environment-variables.1.key="LLM_API_KEY" secret-environment-variables.1.value="$(LLM_API_KEY)" region=$(REGION) > /dev/null; \
	fi; \
	echo "Waiting for backend container to be ready for deployment..."; \
	while true; do \
		STATUS=$$(scw container container get $$CONTAINER_ID region=$(REGION) -o json | jq -r '.status'); \
		if [ "$$STATUS" = "creating" ] || [ "$$STATUS" = "locked" ] || [ "$$STATUS" = "pending" ]; then \
			sleep 2; \
		else \
			break; \
		fi; \
	done; \
	echo "Deploying and waiting for container $$CONTAINER_ID to be ready..."; \
	scw container container deploy $$CONTAINER_ID region=$(REGION) -w

# 5. Deploy Frontend (requires Backend to be deployed first to get its URL)
deploy-frontend: build-push-frontend init-namespace
	@echo "Deploying frontend container..."
	@NAMESPACE_ID=$$(scw container namespace list name=$(REGISTRY_NAME)-ns region=$(REGION) -o json | jq -r '.[0].id'); \
	BACKEND_DOMAIN=$$(scw container container list namespace-id=$$NAMESPACE_ID name=$(BACKEND_IMAGE)-container region=$(REGION) -o json | jq -r '.[0].domain_name // empty'); \
	if [ -z "$$BACKEND_DOMAIN" ]; then \
		echo "Backend container not found. Please run 'make deploy-backend' first."; \
		exit 1; \
	fi; \
	echo "Backend URL found: http://$$BACKEND_DOMAIN"; \
	CONTAINER_ID=$$(scw container container list namespace-id=$$NAMESPACE_ID name=$(FRONTEND_IMAGE)-container region=$(REGION) -o json | jq -r '.[0].id // empty'); \
	if [ -z "$$CONTAINER_ID" ]; then \
		echo "Creating new container..."; \
		CONTAINER_ID=$$(scw container container create namespace-id=$$NAMESPACE_ID name=$(FRONTEND_IMAGE)-container registry-image=$(REGISTRY_ENDPOINT)/$(FRONTEND_IMAGE):$(TAG) port=8080 environment-variables.BACKEND_URL="https://$$BACKEND_DOMAIN" region=$(REGION) -o json | jq -r '.id'); \
	else \
		echo "Updating existing container..."; \
		scw container container update $$CONTAINER_ID registry-image=$(REGISTRY_ENDPOINT)/$(FRONTEND_IMAGE):$(TAG) environment-variables.BACKEND_URL="https://$$BACKEND_DOMAIN" region=$(REGION) > /dev/null; \
	fi; \
	echo "Waiting for frontend container to be ready for deployment..."; \
	while true; do \
		STATUS=$$(scw container container get $$CONTAINER_ID region=$(REGION) -o json | jq -r '.status'); \
		if [ "$$STATUS" = "creating" ] || [ "$$STATUS" = "locked" ] || [ "$$STATUS" = "pending" ]; then \
			sleep 2; \
		else \
			break; \
		fi; \
	done; \
	echo "Deploying and waiting for container $$CONTAINER_ID to be ready..."; \
	scw container container deploy $$CONTAINER_ID region=$(REGION) -w

# 6. Deploy All
deploy-all: deploy-backend deploy-frontend
	@echo "All deployed successfully!"
	@NAMESPACE_ID=$$(scw container namespace list name=$(REGISTRY_NAME)-ns region=$(REGION) -o json | jq -r '.[0].id'); \
	FRONTEND_DOMAIN=$$(scw container container list namespace-id=$$NAMESPACE_ID name=$(FRONTEND_IMAGE)-container region=$(REGION) -o json | jq -r '.[0].domain_name // empty'); \
	echo "Frontend URL: https://$$FRONTEND_DOMAIN"
