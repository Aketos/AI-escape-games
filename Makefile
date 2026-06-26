# Makefile for Scaleway Serverless Deployment (Backend & Frontend)

REGION ?= fr-par
REGISTRY_NAME ?= escape-game-repo
BACKEND_IMAGE ?= escape-game-backend
FRONTEND_IMAGE ?= escape-game-frontend
UNMUTE_VOICE ?= cml-tts/fr/12080_11650_000047-0001_enhanced.wav
LLM_UPSTREAM_URL ?= https://api.scaleway.ai/0cf90930-f182-408a-a3c1-78350db643e4/v1
TAG ?= latest
REGISTRY_ENDPOINT ?= rg.$(REGION).scw.cloud/$(REGISTRY_NAME)

SCW_PROJECT_ID ?= $(shell scw config get default-project-id)

# Load local secrets from gitignored secrets.mk
-include secrets.mk

LLM_MODEL ?= gemma-3-27b-it
LLM_API_KEY ?= $(SCW_SECRET_KEY)

.PHONY: scw-login build-push-backend build-push-frontend deploy-backend deploy-frontend deploy-all init-registry init-namespace new-scenario

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

# 7. Scaffold a new scenario (interactive)
# Usage: make new-scenario
#        (prompts for language and scenario name)
new-scenario:
	@read -p "Language (fr/en) [fr]: " LANG_INPUT; \
	LANG_VAL=$${LANG_INPUT:-fr}; \
	read -p "Scenario ID (e.g. projet_longevite): " SCENARIO_INPUT; \
	if [ -z "$$SCENARIO_INPUT" ]; then echo "Scenario ID is required."; exit 1; fi; \
	DIR=config/$$LANG_VAL/$$SCENARIO_INPUT; \
	if [ -d "$$DIR" ]; then echo "Scenario '$$SCENARIO_INPUT' already exists in $$DIR"; exit 1; fi; \
	echo "Creating scenario '$$SCENARIO_INPUT' in $$DIR/..."; \
	mkdir -p "$$DIR"; \
	\
	echo '{' > "$$DIR/scenario.json"; \
	echo '  "name": "'$$SCENARIO_INPUT'",' >> "$$DIR/scenario.json"; \
	echo '  "description": ""' >> "$$DIR/scenario.json"; \
	echo '}' >> "$$DIR/scenario.json"; \
	\
	printf '%s\n' \
		'You are the game master of an escape room.' \
		'' \
		'(Replace this with your AI persona for this scenario.)' \
		> "$$DIR/persona.txt"; \
	\
	printf '%s\n' \
		'You are starting a new game. Set the scene and atmosphere.' \
		'' \
		'(Replace this with your intro directive for this scenario.)' \
		> "$$DIR/intro_directive.txt"; \
	\
	printf '%s\n' \
		'[INTRO] The game begins. Describe the opening situation to the player.' \
		'' \
		'(Replace this with your intro prompt for this scenario.)' \
		> "$$DIR/intro_prompt.txt"; \
	\
	printf '%s\n' \
		'{' \
		'  "room_id": "starting_room",' \
		'  "name": "Starting Room",' \
		'  "description": "A room.",' \
		'  "items": {}' \
		'}' \
		> "$$DIR/room_state.json"; \
	\
	printf '%s\n' \
		'{' \
		'  "player_id": "p_001",' \
		'  "current_room": "starting_room",' \
		'  "inventory": [],' \
		'  "history": []' \
		'}' \
		> "$$DIR/player_state.json"; \
	\
	printf '%s\n' \
		'{' \
		'  "tools": [' \
		'    {' \
		'      "name": "inspect_item",' \
		'      "description": "Called when the player wants to examine an object.",' \
		'      "parameters": {' \
		'        "type": "object",' \
		'        "properties": {' \
		'          "target_item": {' \
		'            "type": "string",' \
		'            "description": "The ID of the item to inspect."' \
		'          }' \
		'        },' \
		'        "required": ["target_item"]' \
		'      }' \
		'    }' \
		'  ]' \
		'}' \
		> "$$DIR/function_calls.json"; \
	\
	echo "Done! Edit the files in $$DIR/ to define your scenario."; \
	echo "Files created: scenario.json persona.txt intro_directive.txt intro_prompt.txt room_state.json player_state.json function_calls.json"
