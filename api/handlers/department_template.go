package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/linesmerrill/police-cad-api/config"
	"github.com/linesmerrill/police-cad-api/databases"
	"github.com/linesmerrill/police-cad-api/models"
)

// DepartmentTemplate handles department template operations
type DepartmentTemplate struct {
	CommunityDB databases.CommunityDatabase
	TemplateDB  *databases.TemplateDatabase
}

// NewDepartmentTemplate creates a new department template handler
func NewDepartmentTemplate(communityDB databases.CommunityDatabase, templateDB *databases.TemplateDatabase) *DepartmentTemplate {
	return &DepartmentTemplate{
		CommunityDB: communityDB,
		TemplateDB:  templateDB,
	}
}

// CreateDepartmentWithTemplateHandler creates a new department using a global template
func (dt *DepartmentTemplate) CreateDepartmentWithTemplateHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]

	// Parse the request body
	var request struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Image        string `json:"image"`
		TemplateID   string `json:"templateId"`
		Category     string `json:"category"` // Optional: if not provided, will use template's category
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert the community ID to a primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	// Verify community exists
	_, err = dt.CommunityDB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	// Get the template
	var template models.GlobalTemplate
	if request.TemplateID != "" {
		// Use specific template
		templateID, err := primitive.ObjectIDFromHex(request.TemplateID)
		if err != nil {
			config.ErrorStatus("invalid template ID", http.StatusBadRequest, w, err)
			return
		}

		template, err = dt.TemplateDB.FindOne(context.Background(), bson.M{"_id": templateID})
		if err != nil {
			config.ErrorStatus("template not found", http.StatusNotFound, w, err)
			return
		}
	} else {
		// Use default template for the category
		category := request.Category
		if category == "" {
			category = "police" // Default category
		}

		templates, err := dt.TemplateDB.GetTemplatesByCategory(context.Background(), category)
		if err != nil || len(templates) == 0 {
			config.ErrorStatus("no default template found for category", http.StatusNotFound, w, err)
			return
		}

		// Find the first default template for this category
		for _, t := range templates {
			if t.IsDefault {
				template = t
				break
			}
		}

		if template.ID.IsZero() {
			template = templates[0] // Use first available template
		}
	}

	// Create the department
	department := models.Department{
		ID:               primitive.NewObjectID(),
		Name:             request.Name,
		Description:      request.Description,
		Image:            request.Image,
		ApprovalRequired: false,
		Members:          []models.MemberStatus{},
		Template:         models.Template{}, // Empty legacy template
		TemplateRef: &models.TemplateReference{
			TemplateID:     template.ID,
			Customizations: make(map[string]models.ComponentOverride),
			IsActive:       true,
		},
		CreatedAt:         primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt:         primitive.NewDateTimeFromTime(time.Now()),
		OnlineMemberCount: 0,
	}

	// Set up default component customizations based on template component references
	for _, componentRef := range template.Components {
		department.TemplateRef.Customizations[componentRef.ComponentID.Hex()] = models.ComponentOverride{
			Enabled: componentRef.Enabled,
		}
	}

	// Add the department to the community
	filter := bson.M{"_id": cID}
	update := bson.M{"$push": bson.M{"community.departments": department}}
	err = dt.CommunityDB.UpdateOne(context.Background(), filter, update)
	if err != nil {
		config.ErrorStatus("failed to add department to community", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "Department created successfully",
		"department": department,
		"template":   template,
	})
}

// UpdateDepartmentTemplateHandler updates a department's template reference
func (dt *DepartmentTemplate) UpdateDepartmentTemplateHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Parse the request body
	var request struct {
		TemplateID     string                                    `json:"templateId"`
		Customizations map[string]models.ComponentOverride `json:"customizations"`
		IsActive       bool                                      `json:"isActive"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		config.ErrorStatus("failed to decode request body", http.StatusBadRequest, w, err)
		return
	}

	// Convert IDs to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid department ID", http.StatusBadRequest, w, err)
		return
	}

	// Verify template exists if provided
	if request.TemplateID != "" {
		templateID, err := primitive.ObjectIDFromHex(request.TemplateID)
		if err != nil {
			config.ErrorStatus("invalid template ID", http.StatusBadRequest, w, err)
			return
		}

		_, err = dt.TemplateDB.FindOne(context.Background(), bson.M{"_id": templateID})
		if err != nil {
			config.ErrorStatus("template not found", http.StatusNotFound, w, err)
			return
		}
	}

	// Update the department's template reference
	updateFilter := bson.M{
		"_id": cID,
		"community.departments._id": dID,
	}

	updateFields := bson.M{}
	if request.TemplateID != "" {
		templateID, _ := primitive.ObjectIDFromHex(request.TemplateID)
		updateFields["community.departments.$.templateRef.templateId"] = templateID
	}
	if request.Customizations != nil {
		updateFields["community.departments.$.templateRef.customizations"] = request.Customizations
	}
	if request.IsActive {
		updateFields["community.departments.$.templateRef.isActive"] = request.IsActive
	}

	update := bson.M{"$set": updateFields}
	err = dt.CommunityDB.UpdateOne(context.Background(), updateFilter, update)
	if err != nil {
		config.ErrorStatus("failed to update department template", http.StatusInternalServerError, w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Department template updated successfully",
	})
}

// GetDepartmentTemplateHandler retrieves a department's template information
func (dt *DepartmentTemplate) GetDepartmentTemplateHandler(w http.ResponseWriter, r *http.Request) {
	communityID := mux.Vars(r)["communityId"]
	departmentID := mux.Vars(r)["departmentId"]

	// Convert IDs to primitive.ObjectID
	cID, err := primitive.ObjectIDFromHex(communityID)
	if err != nil {
		config.ErrorStatus("invalid community ID", http.StatusBadRequest, w, err)
		return
	}

	dID, err := primitive.ObjectIDFromHex(departmentID)
	if err != nil {
		config.ErrorStatus("invalid department ID", http.StatusBadRequest, w, err)
		return
	}

	// Find the community
	community, err := dt.CommunityDB.FindOne(context.Background(), bson.M{"_id": cID})
	if err != nil {
		config.ErrorStatus("community not found", http.StatusNotFound, w, err)
		return
	}

	// Find the department
	var department *models.Department
	for i, dept := range community.Details.Departments {
		if dept.ID == dID {
			department = &community.Details.Departments[i]
			break
		}
	}

	if department == nil {
		config.ErrorStatus("department not found", http.StatusNotFound, w, nil)
		return
	}

	response := map[string]interface{}{
		"department": department,
	}

	// If department has a template reference, get the full template
	if department.TemplateRef != nil {
		template, err := dt.TemplateDB.FindOne(context.Background(), bson.M{"_id": department.TemplateRef.TemplateID})
		if err == nil {
			response["template"] = template
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}