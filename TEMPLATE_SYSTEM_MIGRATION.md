# Template System Migration Guide

## Overview

This document outlines the migration from embedded templates to a centralized template management system. The new system allows for:

- **Centralized Template Management**: Templates are stored in a separate collection and can be managed independently
- **Global Template Updates**: Changes to templates automatically apply to all communities using them
- **Component Customization**: Departments can customize which components are enabled/disabled
- **Backward Compatibility**: Existing embedded templates continue to work during migration

## New Architecture

### 1. Global Templates Collection

Templates are now stored in a separate `templates` collection with the following structure:

```go
type GlobalTemplate struct {
    ID          primitive.ObjectID `json:"_id" bson:"_id"`
    Name        string             `json:"name" bson:"name"`
    Description string             `json:"description" bson:"description"`
    Category    string             `json:"category" bson:"category"` // "police", "ems", "fire", etc.
    IsDefault   bool               `json:"isDefault" bson:"isDefault"`
    IsActive    bool               `json:"isActive" bson:"isActive"`
    Components  []GlobalComponent  `json:"components" bson:"components"`
    CreatedAt   primitive.DateTime `json:"createdAt" bson:"createdAt"`
    UpdatedAt   primitive.DateTime `json:"updatedAt" bson:"updatedAt"`
    CreatedBy   string             `json:"createdBy" bson:"createdBy"`
}
```

### 2. Template References in Departments

Departments now reference templates instead of embedding them:

```go
type Department struct {
    // ... existing fields ...
    Template    Template           `json:"template" bson:"template"`    // Legacy (backward compatibility)
    TemplateRef *TemplateReference `json:"templateRef" bson:"templateRef"` // New system
}

type TemplateReference struct {
    TemplateID     primitive.ObjectID            `json:"templateId" bson:"templateId"`
    Customizations map[string]ComponentOverride `json:"customizations" bson:"customizations"`
    IsActive      bool                          `json:"isActive" bson:"isActive"`
}
```

## Migration Strategy

### Phase 1: Deploy New System (Current)

1. **New Files Created**:
   - `models/template.go` - New template models
   - `databases/template.go` - Template database operations
   - `api/handlers/template.go` - Template management API
   - `api/handlers/template_migration.go` - Migration utilities
   - `api/handlers/department_template.go` - Department template operations

2. **Backward Compatibility**:
   - Existing embedded templates continue to work
   - New `TemplateRef` field is optional
   - Legacy `Template` field remains for compatibility

### Phase 2: Migrate Existing Communities

Use the migration endpoints to convert existing embedded templates:

```bash
# Migrate a specific community
POST /api/templates/migrate/community/{communityId}

# Migrate all communities
POST /api/templates/migrate/all

# Check migration status
GET /api/templates/migrate/status
```

### Phase 3: Update Application Code

1. **Update Department Creation**: Use new template-based department creation
2. **Update Template Management**: Use centralized template APIs
3. **Remove Legacy Code**: After migration is complete

## API Endpoints

### Template Management

```bash
# Create a new template
POST /api/templates
{
    "name": "Custom Police Template",
    "description": "Custom template for police departments",
    "category": "police",
    "components": [...]
}

# Get all templates (with filtering)
GET /api/templates?category=police&page=1&limit=20

# Get specific template
GET /api/templates/{templateId}

# Update template
PUT /api/templates/{templateId}

# Delete template (non-default only)
DELETE /api/templates/{templateId}

# Get default templates
GET /api/templates/defaults

# Get templates by category
GET /api/templates/category/{category}

# Initialize default templates
POST /api/templates/initialize-defaults
```

### Department Template Operations

```bash
# Create department with template
POST /api/communities/{communityId}/departments/with-template
{
    "name": "Police Department",
    "description": "Main police department",
    "templateId": "template_id_here",
    "category": "police"
}

# Update department template
PUT /api/communities/{communityId}/departments/{departmentId}/template
{
    "templateId": "new_template_id",
    "customizations": {
        "component_id": {
            "enabled": true,
            "settings": {...}
        }
    }
}

# Get department template info
GET /api/communities/{communityId}/departments/{departmentId}/template
```

## Default Templates

The system includes default templates for common department types:

### Police Template
- MDT (Mobile Data Terminal)
- Radio System
- Traffic Stop Module
- Arrest Report System

### EMS Template
- Medical Records System
- Ambulance Dispatch
- Medical Report System

### Fire Template
- Fire Dispatch System
- Fire Report System

## Benefits

1. **Centralized Management**: All templates managed in one place
2. **Consistency**: All communities use the same template versions
3. **Easy Updates**: Update templates once, applies everywhere
4. **Customization**: Departments can still customize component settings
5. **Scalability**: Easy to add new templates and components
6. **Backward Compatibility**: Existing communities continue to work

## Migration Checklist

- [x] Create new template models and database layer
- [x] Create template management API endpoints
- [x] Create migration utilities
- [x] Update Department model for backward compatibility
- [x] Create department template operations
- [ ] Add template routes to main router
- [ ] Initialize default templates in database
- [ ] Test migration with sample data
- [ ] Deploy to staging environment
- [ ] Run migration on production data
- [ ] Update frontend to use new template system
- [ ] Remove legacy template code (after migration complete)

## Next Steps

1. **Add Routes**: Integrate new endpoints into the main router
2. **Initialize Defaults**: Run the default template initialization
3. **Test Migration**: Test with sample communities
4. **Frontend Updates**: Update frontend to use new template APIs
5. **Production Migration**: Run migration on production data

## Rollback Plan

If issues arise during migration:

1. **Stop Migration**: Disable migration endpoints
2. **Revert Code**: Deploy previous version
3. **Data Integrity**: Existing embedded templates remain intact
4. **Investigate**: Fix issues before retrying migration

The system is designed to be safe - existing data is never modified, only new references are added.
