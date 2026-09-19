package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"biosample-cold-custody-tracking/backend/internal/dto"
	"biosample-cold-custody-tracking/backend/internal/repository"
	"biosample-cold-custody-tracking/backend/internal/service"
	"biosample-cold-custody-tracking/backend/internal/util"
)

type TemperatureAnomalyHandler struct{ service service.AnomalyService }

func NewTemperatureAnomalyHandler(anomalyService service.AnomalyService) *TemperatureAnomalyHandler {
	return &TemperatureAnomalyHandler{service: anomalyService}
}

func (h *TemperatureAnomalyHandler) List(c *gin.Context) {
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

func (h *TemperatureAnomalyHandler) Get(c *gin.Context) {
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

func (h *TemperatureAnomalyHandler) Report(c *gin.Context) {
	var input dto.CreateTemperatureAnomalyRequest
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

func (h *TemperatureAnomalyHandler) Resolve(c *gin.Context) {
	id, ok := util.ParseID(c)
	if !ok {
		return
	}
	var input dto.ResolveTemperatureAnomalyRequest
	if !util.BindJSON(c, &input) {
		return
	}
	item, err := h.service.Resolve(c.Request.Context(), ActorFromContext(c), id, input)
	if err != nil {
		util.RespondError(c, err)
		return
	}
	util.Respond(c, http.StatusOK, item)
}
