COVERAGE_FILE ?= $$(docker images | awk '{print $$3}' | awk 'NR==2')
API_KEY ?=сука, я это дерьмо 6 часов писал
SERVER ?=АМЕРИКА СОСАТЬ, СОСАТЬ АМЕРИКА!
.PHONY: deploy
deploy:
	docker build -t queue-app . 
	docker save -o docker_image $(shell docker images | awk '{print $$1}' | awk 'NR==2')  
	scp docker_image $(SERVER):~/Queue_for_labs
	ssh $(SERVER) "cd Queue_for_labs && docker load -i docker_image && docker run -d -e API_KEY=$(API_KEY) $(COVERAGE_FILE) && docker system prune"
	
	
