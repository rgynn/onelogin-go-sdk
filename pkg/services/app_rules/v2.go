package apprules

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onelogin/onelogin-go-sdk/internal/customerrors"
	"github.com/onelogin/onelogin-go-sdk/pkg/services"
	"github.com/onelogin/onelogin-go-sdk/pkg/services/olhttp"
	"github.com/onelogin/onelogin-go-sdk/pkg/utils"
	"log"
	"sync"
)

const errAppsV2Context = "app rules v2 service"

// V2Service holds the information needed to interface with a repository
type V2Service struct {
	Endpoint, ErrorContext string
	Repository             services.Repository
	LegalValuesService     services.SimpleQuery
}

// New creates the new svc service v2.
func New(repo services.Repository, legalValues services.SimpleQuery, host string) *V2Service {
	return &V2Service{
		Endpoint:           fmt.Sprintf("%s/api/2/apps", host),
		Repository:         repo,
		ErrorContext:       errAppsV2Context,
		LegalValuesService: legalValues,
	}
}

func (svc *V2Service) Query(query *AppRuleQuery) ([]AppRule, error) {
	resp, err := svc.Repository.Read(olhttp.OLHTTPRequest{
		URL:        fmt.Sprintf("%s/%s/rules", svc.Endpoint, query.AppID),
		Headers:    map[string]string{"Content-Type": "application/json"},
		AuthMethod: "bearer",
	})
	if err != nil {
		return nil, err
	}

	var appRules []AppRule
	json.Unmarshal(resp, &appRules)
	return appRules, nil
}

func (svc *V2Service) GetOne(appId int32, id int32) (*AppRule, error) {
	resp, err := svc.Repository.Read(olhttp.OLHTTPRequest{
		URL:        fmt.Sprintf("%s/%d/rules/%d", svc.Endpoint, appId, id),
		Headers:    map[string]string{"Content-Type": "application/json"},
		AuthMethod: "bearer",
	})
	if err != nil {
		return nil, err
	}

	var appRule AppRule
	json.Unmarshal(resp, &appRule)
	return &appRule, nil
}

func (svc *V2Service) Create(appRule *AppRule) error {
	resp, err := svc.Repository.Create(olhttp.OLHTTPRequest{
		URL:        fmt.Sprintf("%s/%d/rules", svc.Endpoint, *appRule.AppID),
		Headers:    map[string]string{"Content-Type": "application/json"},
		AuthMethod: "bearer",
		Payload:    appRule,
	})
	if err != nil {
		return err
	}
	json.Unmarshal(resp, appRule)
	return nil
}

func (svc *V2Service) Update(appRule *AppRule) error {
	if appRule.ID == nil {
		return errors.New("No ID Given")
	}
	validationErr := validateRuleValues(appRule, svc.LegalValuesService)
	if validationErr != nil {
		fmt.Println(validationErr)
		return validationErr
	}
	resp, err := svc.Repository.Update(olhttp.OLHTTPRequest{
		URL:        fmt.Sprintf("%s/%d/rules/%d", svc.Endpoint, *appRule.AppID, *appRule.ID),
		Headers:    map[string]string{"Content-Type": "application/json"},
		AuthMethod: "bearer",
		Payload:    appRule,
	})
	if err != nil {
		return err
	}
	json.Unmarshal(resp, appRule)
	return nil
}

func (svc *V2Service) Destroy(appId int32, id int32) error {
	if _, err := svc.Repository.Destroy(olhttp.OLHTTPRequest{
		URL:        fmt.Sprintf("%s/%d/rules/%d", svc.Endpoint, appId, id),
		Headers:    map[string]string{"Content-Type": "application/json"},
		AuthMethod: "bearer",
	}); err != nil {
		return err
	}
	return nil
}

func validateRuleValues(rule *AppRule, svc services.SimpleQuery) error {
	legalValRequests := map[string][]string{}
	legalValRequests["rules/conditions"] = []string{}
	legalValRequests["rules/actions"] = []string{}
	for _, condition := range rule.Conditions {
		legalValRequests[fmt.Sprintf("rules/conditions/%s/values", *condition.Source)] = []string{}
		legalValRequests[fmt.Sprintf("rules/conditions/%s/operators", *condition.Source)] = []string{}
	}
	for _, action := range rule.Actions {
		legalValRequests[fmt.Sprintf("rules/actions/%s/values", *action.Action)] = []string{}
	}

	var (
		wg    sync.WaitGroup
		mutex = &sync.Mutex{}
	)
	for reqURL := range legalValRequests {
		wg.Add(1)
		go func(reqURL string, legalValRequest map[string][]string) {
			defer wg.Done()
			legalValResp := []map[string]string{}
			err := svc.Query(reqURL, &legalValResp)
			if err != nil {
				log.Println("Problem validating rule", reqURL, err)
			}
			legalVals := make([]string, len(legalValResp))
			for i, legalVal := range legalValResp {
				legalVals[i] = legalVal["value"]
			}
			mutex.Lock()
			legalValRequests[reqURL] = legalVals
			mutex.Unlock()
		}(reqURL, legalValRequests)
	}
	wg.Wait()

	errorMsgs := make([]error, 0)
	for _, condition := range rule.Conditions {
		if len(legalValRequests["rules/conditions"]) > 0 {
			err := utils.OneOf(fmt.Sprintf("%s.conditions.source", *rule.Name), *condition.Source, legalValRequests["rules/conditions"])
			if err != nil {
				log.Println("Illegal value given for condition source")
				errorMsgs = append(errorMsgs, err)
			}
		}
		if len(legalValRequests[fmt.Sprintf("rules/conditions/%s/values", *condition.Source)]) > 0 {
			err := utils.OneOf(fmt.Sprintf("%s.conditions.value", *rule.Name), *condition.Value, legalValRequests[fmt.Sprintf("rules/conditions/%s/values", *condition.Source)])
			if err != nil {
				log.Println("Illegal value given for condition value")
				errorMsgs = append(errorMsgs, err)
			}
		}
		if len(legalValRequests[fmt.Sprintf("rules/conditions/%s/operators", *condition.Source)]) > 0 {
			err := utils.OneOf(fmt.Sprintf("%s.conditions.operator", *rule.Name), *condition.Operator, legalValRequests[fmt.Sprintf("rules/conditions/%s/operators", *condition.Source)])
			if err != nil {
				log.Println("Illegal value given for condition operator")
				errorMsgs = append(errorMsgs, err)
			}
		}
	}

	for _, action := range rule.Actions {
		if len(legalValRequests["rules/actions"]) > 0 {
			err := utils.OneOf(fmt.Sprintf("%s.actions.action", *rule.Name), *action.Action, legalValRequests["rules/actions"])
			if err != nil {
				log.Println("Illegal value given for action")
				errorMsgs = append(errorMsgs, err)
			}
		}
		for _, val := range action.Value {
			if len(legalValRequests[fmt.Sprintf("rules/actions/%s/values", *action.Action)]) > 0 {
				err := utils.OneOf(fmt.Sprintf("%s.actions.values", *rule.Name), val, legalValRequests[fmt.Sprintf("rules/actions/%s/values", *action.Action)])
				if err != nil {
					log.Println("Illegal value given for action value")
					errorMsgs = append(errorMsgs, err)
				}
			}
		}
	}
	return customerrors.StackErrors(errorMsgs)
}