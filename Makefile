.PHONY: bashly shellcheck

bashly:
	@docker run --rm -it -u $(id -u):$(id -g) -v "${PWD}:/app" -e BASHLY_SETTINGS_PATH="src/settings.yml" dannyben/bashly $(filter-out $@,$(MAKECMDGOALS))
shellcheck:
	@docker run --rm -v "${PWD}:/mnt" koalaman/shellcheck:stable dist/spark $(filter-out $@,$(MAKECMDGOALS))
%:
	@:
