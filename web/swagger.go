package web

import (
	"encoding/json"
	"fmt"
)

type swaggerURL struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

func swaggerHTML(docsPath string, groups []string) string {
	urls := []swaggerURL{{URL: docsPath, Name: "default"}}
	for _, group := range groups {
		if group == "default" {
			continue
		}
		urls = append(urls, swaggerURL{URL: docsPath + "/" + group, Name: group})
	}
	raw, err := json.Marshal(urls)
	if err != nil {
		raw = []byte(`[{"url":"/v3/api-docs","name":"default"}]`)
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>API documentation</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function () {
      SwaggerUIBundle({
        urls: %s,
        dom_id: '#swagger-ui',
        deepLinking: true,
        displayRequestDuration: true
      });
    };
  </script>
</body>
</html>
`, raw)
}
