package mailbox

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// respondOrganizeError answers a body that broke the contract's rules with 422 and field errors,
// and any other error through the error mapping.
func respondOrganizeError(c *gin.Context, message string, err error) {
	if validation, ok := asValidationError(err); ok {
		c.JSON(http.StatusUnprocessableEntity, response.ValidationErrorResponse{
			Code:      http.StatusUnprocessableEntity,
			ErrorCode: domain.CodeValidationFailed,
			Message:   "Validation failed",
			Errors:    validation.Fields,
			RequestID: c.GetString("request_id"),
		})
		return
	}
	response.HandleError(c, message, err)
}

// UpdateMessages applies one change to up to 100 messages of a mailbox.
func (h *Handler) UpdateMessages(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req UpdateMailMessagesRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	updated, err := h.service.UpdateMessages(c.Request.Context(), viewer, mailboxID, req.MessageIDs, req.update())
	if err != nil {
		respondOrganizeError(c, "Failed to update mail messages", err)
		return
	}
	privateResponse(c)
	response.Success(c, MailMessageUpdatesResponse{Updated: updated})
}

// PurgeTrash empties the trash of a mailbox.
func (h *Handler) PurgeTrash(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	deleted, err := h.service.PurgeTrash(c.Request.Context(), viewer, mailboxID)
	if err != nil {
		response.HandleError(c, "Failed to empty the trash", err)
		return
	}
	privateResponse(c)
	response.Success(c, MailTrashPurgeResponse{Deleted: deleted})
}

func (h *Handler) ListFolders(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	folders, err := h.service.ListFolders(c.Request.Context(), viewer, mailboxID)
	if err != nil {
		response.HandleError(c, "Failed to list mail folders", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailFolderResponses(folders))
}

func (h *Handler) CreateFolder(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req MailFolderRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	folder, err := h.service.CreateFolder(c.Request.Context(), viewer, mailboxID, req.Name)
	if err != nil {
		respondOrganizeError(c, "Failed to create mail folder", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailFolderResponse(folder))
}

func (h *Handler) RenameFolder(c *gin.Context) {
	viewer, mailboxID, folderID, ok := folderScope(c)
	if !ok {
		return
	}
	var req MailFolderRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	folder, err := h.service.RenameFolder(c.Request.Context(), viewer, mailboxID, folderID, req.Name)
	if err != nil {
		respondOrganizeError(c, "Failed to rename mail folder", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailFolderResponse(folder))
}

func (h *Handler) DeleteFolder(c *gin.Context) {
	viewer, mailboxID, folderID, ok := folderScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteFolder(c.Request.Context(), viewer, mailboxID, folderID); err != nil {
		response.HandleError(c, "Failed to delete mail folder", err)
		return
	}
	response.NoContent(c)
}

func (h *Handler) ListLabels(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	labels, err := h.service.ListLabels(c.Request.Context(), viewer, mailboxID)
	if err != nil {
		response.HandleError(c, "Failed to list mail labels", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailLabelResponses(labels))
}

func (h *Handler) CreateLabel(c *gin.Context) {
	viewer, mailboxID, ok := mailboxScope(c)
	if !ok {
		return
	}
	var req MailLabelRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	label, err := h.service.CreateLabel(c.Request.Context(), viewer, mailboxID, req.Name, domain.MailLabelColor(req.Color))
	if err != nil {
		respondOrganizeError(c, "Failed to create mail label", err)
		return
	}
	privateResponse(c)
	response.Created(c, toMailLabelResponse(label))
}

func (h *Handler) UpdateLabel(c *gin.Context) {
	viewer, mailboxID, labelID, ok := labelScope(c)
	if !ok {
		return
	}
	var req MailLabelRequest
	if !httphandler.BindJSON(c, &req) {
		return
	}
	label, err := h.service.UpdateLabel(c.Request.Context(), viewer, mailboxID, labelID, req.Name, domain.MailLabelColor(req.Color))
	if err != nil {
		respondOrganizeError(c, "Failed to update mail label", err)
		return
	}
	privateResponse(c)
	response.Success(c, toMailLabelResponse(label))
}

func (h *Handler) DeleteLabel(c *gin.Context) {
	viewer, mailboxID, labelID, ok := labelScope(c)
	if !ok {
		return
	}
	if err := h.service.DeleteLabel(c.Request.Context(), viewer, mailboxID, labelID); err != nil {
		response.HandleError(c, "Failed to delete mail label", err)
		return
	}
	response.NoContent(c)
}

func folderScope(c *gin.Context) (viewer domain.OrganizationContext, mailboxID, folderID uint, ok bool) {
	viewer, mailboxID, ok = mailboxScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, 0, false
	}
	folderID, ok = httphandler.ParseID(c, "folder_id")
	return viewer, mailboxID, folderID, ok
}

func labelScope(c *gin.Context) (viewer domain.OrganizationContext, mailboxID, labelID uint, ok bool) {
	viewer, mailboxID, ok = mailboxScope(c)
	if !ok {
		return domain.OrganizationContext{}, 0, 0, false
	}
	labelID, ok = httphandler.ParseID(c, "label_id")
	return viewer, mailboxID, labelID, ok
}
