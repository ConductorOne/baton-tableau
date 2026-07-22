package connector

import (
	v2 "github.com/conductorone/baton-sdk/pb/c1/connector/v2"
	"github.com/conductorone/baton-sdk/pkg/annotations"
)

var (
	resourceTypeSite = &v2.ResourceType{
		Id:          "site",
		DisplayName: "Site",
	}
	resourceTypeUser = &v2.ResourceType{
		Id:          "user",
		DisplayName: "User",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_USER,
		},
		Annotations: annotations.New(&v2.SkipEntitlementsAndGrants{}),
	}
	resourceTypeGroup = &v2.ResourceType{
		Id:          "group",
		DisplayName: "Group",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_GROUP,
		},
	}
	resourceTypeLicense = &v2.ResourceType{
		Id:          "license",
		DisplayName: "License",
		Traits: []v2.ResourceType_Trait{
			v2.ResourceType_TRAIT_ROLE,
			v2.ResourceType_TRAIT_LICENSE_PROFILE,
		},
	}
	resourceTypeProject = &v2.ResourceType{
		Id:          "project",
		DisplayName: "Project",
		Description: "A Tableau project that contains workbooks",
	}
	resourceTypeWorkbook = &v2.ResourceType{
		Id:          "workbook",
		DisplayName: "Workbook",
		Description: "A Tableau workbook that contains views/dashboards",
	}
	resourceTypeView = &v2.ResourceType{
		Id:          "view",
		DisplayName: "View",
		Description: "A Tableau dashboard/view",
	}
)
