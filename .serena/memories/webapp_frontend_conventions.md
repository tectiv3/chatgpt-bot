# Webapp Frontend Conventions

## Vue.js Setup
- **Location**: `webapp/templates/miniapp.html` (template) + `webapp/assets/js/app.js` (logic)
- **Vue Version**: Vue 3 (loaded via CDN)
- **CRITICAL**: Vue uses custom delimiters `[[ ]]` instead of `{{ }}` to avoid conflict with Go's html/template
- Template interpolation: `[[ variable ]]` NOT `{{ variable }}`
- Directives work normally: `v-if`, `v-for`, `:class`, `@click`, etc.

## File Upload Feature
- Supports images + files (PDF, CSV, source code: py, php, go, js, vue, ts, tsx, md, json, yaml, xml, html, css, sql, sh)
- Size limits:
  - Images & PDFs: 10MB max
  - Text/code files: 2MB max
- Frontend validation in `validateFile()` method
- Backend validation in `webapp.go` upload handler

## OpenAI API File Handling (webapp.go `convertMessagesToOpenAI`)
- **Images**: Use `NewChatMessageContentWithBytes()` - sends as image_url
- **PDFs**: Use `NewChatMessageContentFileWithBytes()` - sends via file API
- **Text/code files**: OpenAI doesn't support them in file API! Must prepend content as markdown code block to message text
- Distinguish file vs image using `message.MessageType == "file"` (NOT by checking filename - both have filenames)

## Message Types
- `message_type` field values: "normal", "file", "image", "summary"
- Stored in database and returned via API
- Used in template to render appropriate UI (image tag vs file icon)

## File Icon Helper
- `getFileIconClass(input)` - accepts either filename or mimeType
- Returns FontAwesome class with color (e.g., `fas fa-file-csv text-green-500`)
- Extracts extension from filename if input contains `.` but no `/`
