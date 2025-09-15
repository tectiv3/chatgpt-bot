# URL Citation Annotations Implementation Plan

## Current State Analysis

### Existing Annotation System
The chatbot currently supports `container_file_citation` annotations from OpenAI's Response API:

**Current Database Schema (in `ChatMessage` model):**
- `AnnotationContainerID` - OpenAI container ID
- `AnnotationFileID` - File ID within container
- `AnnotationFilename` - Original filename
- `AnnotationFileType` - File type (image, document, etc.)
- `AnnotationFilePath` - Local storage path

**Current Processing Flow:**
1. OpenAI returns content with annotations
2. For `container_file_citation` types:
   - File is downloaded from OpenAI
   - Stored locally in `uploads/annotations/`
   - Metadata stored in individual database columns
3. Webapp displays files as downloadable links or images

**Current Supported Types:**
- `container_file_citation` only
- `url_citation` is NOT currently supported

## Implementation Requirements

### 1. Add URL Citation Support
Need to support annotations with this structure:
```json
{
    "type": "url_citation",
    "start_index": 2606,
    "end_index": 2758,
    "url": "https://...",
    "title": "Title..."
}
```

### 2. Database Schema Redesign
Replace individual annotation columns with a single JSON column to support multiple annotation types and multiple annotations per message.

### 3. Display Requirements
- Show url_citation annotations as numbered links under message content
- Display inside message bubble for good UX
- Maintain backward compatibility for existing file annotations

## Files That Need Modification

### Backend Files
1. **`models.go`** - Database schema changes for ChatMessage model
2. **`llm.go`** - Annotation processing logic updates
3. **`webapp.go`** - Web API response handling and annotation processing
4. **Database migration** - New migration file needed

### Frontend Files  
1. **`webapp/templates/miniapp.html`** - Display logic for new annotation types
2. **`webapp/assets/js/app.js`** - JavaScript handling for new annotation structure

## Detailed Implementation Plan

### Phase 1: Database Schema Changes

#### 1.1 Update ChatMessage Model (`models.go`)

**Remove existing annotation fields:**
```go
// Remove these fields:
// AnnotationContainerID *string
// AnnotationFileID      *string  
// AnnotationFilename    *string
// AnnotationFileType    *string
// AnnotationFilePath    *string
```

**Add new JSON column:**
```go
// Add this field:
Annotations *string `json:"annotations,omitempty" gorm:"type:text;nullable"`
```

#### 1.2 Create Annotation Types
Add new structs to support different annotation types:

```go
type MessageAnnotations struct {
    UrlCitations     []UrlCitationAnnotation     `json:"url_citations,omitempty"`
    FileCitations    []FileCitationAnnotation    `json:"file_citations,omitempty"`
}

type UrlCitationAnnotation struct {
    Type       string `json:"type"`           // "url_citation"
    StartIndex int    `json:"start_index"`
    EndIndex   int    `json:"end_index"`
    URL        string `json:"url"`
    Title      string `json:"title"`
}

type FileCitationAnnotation struct {
    Type          string `json:"type"`              // "container_file_citation"
    ContainerID   string `json:"container_id"`
    FileID        string `json:"file_id"`
    Filename      string `json:"filename"`
    FileType      string `json:"file_type"`
    FilePath      string `json:"file_path"`
    URL           string `json:"url"`               // Generated URL for frontend
}
```

#### 1.3 Database Migration
Create migration to:
1. Add new `annotations` column as TEXT/JSON
2. Migrate existing annotation data to new format
3. Remove old annotation columns after successful migration

### Phase 2: Backend Processing Changes

#### 2.1 Update LLM Processing (`llm.go`)

**Modify `checkAnnotations` function:**
- Process both `url_citation` and `container_file_citation` types
- Store all annotations in new JSON format

**Modify `processAnnotation` function:**
- Handle url_citation types (no file download needed)
- Continue handling container_file_citation types
- Store data in new JSON structure

#### 2.2 Update Webapp Processing (`webapp.go`)

**Modify `processWebappAnnotations` function:**
- Process url_citation annotations (no file operations)
- Continue processing file citations with download/storage
- Build unified annotations JSON structure

**Update MessageResponse struct:**
- Replace individual annotation fields with `Annotations` field
- Add JSON serialization logic

### Phase 3: Frontend Display Changes

#### 3.1 Update HTML Template (`webapp/templates/miniapp.html`)

**Replace existing annotation display:**
```html
<!-- Current single annotation display -->
<div v-if="message.annotation_file_id" class="mt-3 pr-8">
  <!-- ... existing code ... -->
</div>
```

**With new multiple annotations display:**
```html
<!-- New annotations display -->
<div v-if="message.annotations" class="mt-3 pr-8">
  <!-- URL Citations -->
  <div v-if="message.annotations.url_citations && message.annotations.url_citations.length > 0" 
       class="url-citations mb-2">
    <div class="text-xs opacity-70 mb-1" 
         :class="message.role === 'user' ? 'text-white' : 'text-tg-hint'">
      References:
    </div>
    <div class="flex flex-wrap gap-1">
      <a v-for="(citation, index) in message.annotations.url_citations" 
         :key="index"
         :href="citation.url"
         target="_blank"
         rel="noopener noreferrer"
         class="citation-link inline-flex items-center gap-1 px-2 py-1 text-xs rounded transition-colors"
         :class="message.role === 'user' 
           ? 'bg-white/20 hover:bg-white/30 text-white' 
           : 'bg-tg-link/10 hover:bg-tg-link/20 text-tg-link'"
         :title="citation.title">
        <span>[[index + 1]]</span>
        <i class="fas fa-external-link-alt text-xs"></i>
      </a>
    </div>
  </div>
  
  <!-- File Citations -->
  <div v-if="message.annotations.file_citations && message.annotations.file_citations.length > 0">
    <div v-for="file in message.annotations.file_citations" :key="file.file_id">
      <!-- Image files -->
      <div v-if="file.file_type === 'image'" class="annotation-image mb-2">
        <!-- ... image display code ... -->
      </div>
      
      <!-- Other files -->
      <div v-else class="annotation-file mb-2">
        <!-- ... file download code ... -->
      </div>
    </div>
  </div>
</div>
```

#### 3.2 Update JavaScript (`webapp/assets/js/app.js`)

**Modify message update handling:**
- Replace individual annotation field updates
- Add annotations object handling
- Parse JSON annotations structure

### Phase 4: Migration Strategy

#### 4.1 Data Migration Approach
1. **Backward Compatibility**: Keep old columns during transition
2. **Migration Script**: Convert existing annotation data to new JSON format
3. **Gradual Rollout**: Support both old and new formats during transition
4. **Cleanup**: Remove old columns after successful migration

#### 4.2 Migration Steps
1. Add new `annotations` column
2. Create migration script to populate new column from existing data
3. Update code to write to both old and new formats temporarily
4. Update frontend to read from new format with fallback to old
5. Verify all data migrated correctly
6. Remove old columns and compatibility code

## Testing Strategy

### 4.1 Backend Testing
- Test url_citation annotation processing
- Test file_citation annotation processing (existing functionality)
- Test mixed annotation types in single message
- Test migration script with real data

### 4.2 Frontend Testing
- Test URL citation display formatting
- Test numbered link generation
- Test external link functionality
- Test responsive layout with multiple citations
- Test backward compatibility with existing file annotations

### 4.3 Integration Testing
- Test full flow: OpenAI response → storage → display
- Test webapp real-time updates with new annotations
- Test file uploads still work with new schema

## Risk Mitigation

### 4.1 Database Risks
- **Risk**: Data loss during migration
- **Mitigation**: Backup database before migration, keep old columns during transition

### 4.2 Compatibility Risks  
- **Risk**: Breaking existing annotation display
- **Mitigation**: Maintain backward compatibility, gradual rollout

### 4.3 Performance Risks
- **Risk**: JSON column queries slower than individual columns
- **Mitigation**: Add indexes on commonly queried fields, monitor performance

## Implementation Timeline

1. **Phase 1**: Database schema changes (2-3 hours)
2. **Phase 2**: Backend processing updates (3-4 hours)  
3. **Phase 3**: Frontend display changes (2-3 hours)
4. **Phase 4**: Migration and testing (2-3 hours)

**Total Estimated Time**: 9-13 hours

## Success Criteria

1. ✅ URL citations display as numbered links under messages
2. ✅ Existing file annotations continue to work
3. ✅ Multiple annotations per message supported
4. ✅ Backward compatibility maintained during transition
5. ✅ No data loss during migration
6. ✅ Performance remains acceptable