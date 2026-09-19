package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/service"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type AnomalyHandler struct{ service service.AnomalyService }

func NewAnomalyHandler(anomalyService service.AnomalyService) *AnomalyHandler {
	return &AnomalyHandler{service: anomalyService}
}

func (h *AnomalyHandler) List(c *gin.Context) {
	var filter repository.AnomalyFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		util.RespondError(c, util.BadRequest(err.Error()))
		return
	}
	result, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, result)
}

func (h *AnomalyHandler) Get(c *gin.Context) {
	id, ok := util.ParseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, item)
}

func (h *AnomalyHandler) Report(c *gin.Context) {
	var input dto.CreateAnomalyRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Report(c.Request.Context(), ActorFromContext(c), input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusCreated, item)
}

func (h *AnomalyHandler) Release(c *gin.Context) {
	id, ok := util.ParseID(c)
	if !ok {
		return
	}
	var input dto.ReleaseAnomalyRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Release(c.Request.Context(), ActorFromContext(c), id, input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, item)
}
