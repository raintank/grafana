package api

import "github.com/gin-gonic/gin"

func init() {
	addRoutes(func(self *HttpServer) {
		self.addRoute("POST", "/api/account/collaborators/add", self.addCollaborator)
	})
}

type addCollaboratorDto struct {
	Email string `json:"email" binding:"required"`
}

func (self *HttpServer) addCollaborator(c *gin.Context, auth *authContext) {
	var model addCollaboratorDto

	if !c.EnsureBody(&model) {
		c.JSON(400, gin.H{"status": "Collaborator not found"})
		return
	}

	collaborator, err := self.store.GetUserAccountLogin(model.Email)
	if err != nil {
		c.JSON(404, gin.H{"status": "Collaborator not found"})
		return
	}

	userAccount := auth.userAccount

	if collaborator.Id == userAccount.Id {
		c.JSON(400, gin.H{"status": "Cannot add yourself as collaborator"})
		return
	}

	err = userAccount.AddCollaborator(collaborator.Id)
	if err != nil {
		c.JSON(400, gin.H{"status": err.Error()})
		return
	}

	self.store.SaveUserAccount(userAccount)

	c.JSON(200, gin.H{"status": "Collaborator added"})
}
