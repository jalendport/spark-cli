package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestComposeTemplateMatchesAssets guards the inline composeTemplate copy
// against drift from the real templates in the asset repo. It only runs when
// SPARK_LAB_ASSETS points at a local checkout (the same override the engine
// itself honors), so CI without the asset repo skips it rather than failing.
func TestComposeTemplateMatchesAssets(t *testing.T) {
	assets := os.Getenv(assetsEnv)
	if assets == "" {
		t.Skipf("%s not set — cannot verify the inline template copy against the asset repo", assetsEnv)
	}
	for _, major := range []string{"craft-4", "craft-5"} {
		path := filepath.Join(assets, "skeleton", major, "compose.yaml.tpl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != composeTemplate {
			t.Errorf("inline composeTemplate has drifted from %s — update the copy in this test so dbBlock is guarded against the real placeholder context", path)
		}
	}
}

// composeTemplate mirrors skeleton/craft-{4,5}/compose.yaml.tpl (byte-identical
// across the two majors). It lives here so the test can guard the hand-built,
// hand-indented dbBlock (instance.go) against the exact {{DB_BLOCK}} placeholder
// context — a stray space there yields an invalid compose file with no other
// compile- or test-time check — without reaching into the asset repo.
const composeTemplate = `name: {{PROJECT}}

services:
  nginx:
    image: jalendport/spark-nginx:{{NGINX_TAG}}
    depends_on:
      - php
    init: true
    ports:
      - "127.0.0.1:{{WEB_PORT}}:80"
    volumes:
      - ./web:/app/web

  php:
    build:
      context: ./.docker/php
      args:
        BASE_IMAGE: jalendport/spark-php:{{PHP_TAG}}-fpm
        UID: {{UID}}
        GID: {{GID}}
    depends_on:
      - {{DB_SERVER}}
    init: true
    expose:
      - "9000"
    environment:
      COMPOSER_CACHE_DIR: /composer-cache
      LAB_PLUGIN_DIR: /plugin
    volumes:
      - .:/app
      - ../..:/plugin:ro
      - composer-cache:/composer-cache

{{DB_BLOCK}}
  redis:
    image: jalendport/spark-redis:7.2
    healthcheck:
      test: ["CMD", "redis-cli", "-a", "root", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5
      start_period: 5s
    init: true

  mailpit:
    image: axllent/mailpit:latest
    environment:
      MP_MAX_MESSAGES: 5000
      MP_SMTP_AUTH_ACCEPT_ANY: "true"
      MP_SMTP_AUTH_ALLOW_INSECURE: "true"
    init: true
    ports:
      - "127.0.0.1:{{MAILPIT_PORT}}:8025"

volumes:
  db-data:
  composer-cache:
    external: true
    name: spark-craft-lab-composer
`

// renderCompose substitutes the template placeholders the way mint does,
// injecting the engine-built dbBlock for the given driver.
func renderCompose(spec dbSpec) string {
	mapping := map[string]string{
		"{{PROJECT}}":      "lab-foo-latest-" + spec.suffix + "-abc123",
		"{{NGINX_TAG}}":    "1.26",
		"{{WEB_PORT}}":     "8100",
		"{{MAILPIT_PORT}}": "8101",
		"{{PHP_TAG}}":      "8.2",
		"{{UID}}":          "501",
		"{{GID}}":          "20",
		"{{DB_SERVER}}":    spec.service,
		"{{DB_BLOCK}}":     dbBlock(spec),
	}
	out := composeTemplate
	for k, v := range mapping {
		out = strings.ReplaceAll(out, k, v)
	}
	return out
}

// composeDoc is enough of the compose schema to prove the render parses and the
// db service landed at the right indentation with its nested blocks intact.
type composeDoc struct {
	Name     string `yaml:"name"`
	Services map[string]struct {
		Image       string         `yaml:"image"`
		Environment map[string]any `yaml:"environment"`
		Healthcheck map[string]any `yaml:"healthcheck"`
		Volumes     []string       `yaml:"volumes"`
		Ports       []string       `yaml:"ports"`
	} `yaml:"services"`
}

func TestComposeRenderUnmarshals(t *testing.T) {
	// dbBlock is driver-specific but major-agnostic (the compose template is
	// identical across craft-4/craft-5), so iterating the two drivers covers the
	// meaningful matrix.
	for _, key := range []string{"mysql", "pgsql"} {
		key := key
		t.Run(key, func(t *testing.T) {
			spec := dbSpecs[key]
			rendered := renderCompose(spec)

			var doc composeDoc
			if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
				t.Fatalf("rendered compose is not valid YAML: %v\n%s", err, rendered)
			}

			svc, ok := doc.Services[spec.service]
			if !ok {
				t.Fatalf("no %q service in rendered compose\n%s", spec.service, rendered)
			}
			if svc.Image != spec.image {
				t.Errorf("db image = %q, want %q", svc.Image, spec.image)
			}
			if len(svc.Healthcheck) == 0 {
				t.Errorf("db service has no healthcheck — dbBlock indentation regression?")
			}
			if len(svc.Volumes) == 0 {
				t.Errorf("db service has no volumes — dbBlock indentation regression?")
			}
			if len(svc.Environment) == 0 {
				t.Errorf("db service has no environment — dbBlock indentation regression?")
			}

			// The loopback bind must survive as a single, correctly-shaped mapping.
			if got := doc.Services["nginx"].Ports; len(got) != 1 || got[0] != "127.0.0.1:8100:80" {
				t.Errorf("nginx ports = %v, want [127.0.0.1:8100:80]", got)
			}
			if got := doc.Services["mailpit"].Ports; len(got) != 1 || got[0] != "127.0.0.1:8101:8025" {
				t.Errorf("mailpit ports = %v, want [127.0.0.1:8101:8025]", got)
			}
		})
	}
}
