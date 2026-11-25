package composition

import (
	"context"

	"github.com/Zubrium/cdn/pkg/contracts"
)

type adminFormsAdapter struct {
	dispatcher contracts.Dispatcher
}

func (adapter *adminFormsAdapter) subject(ctx context.Context) (contracts.AuthorizationSubject, error) {
	subject, ok := contracts.AdminSubjectFromContext(ctx)
	if !ok {
		return contracts.AuthorizationSubject{}, contracts.ErrAdminFormsUnauthorized
	}
	return subject, nil
}

func (adapter *adminFormsAdapter) Authenticate(ctx context.Context, credential string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsAuthenticateOperation(), contracts.AdminAuthenticateRequest{Credential: credential})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminFormsAdapter) AuthenticateSession(ctx context.Context, token string) (context.Context, error) {
	if adapter == nil || adapter.dispatcher == nil {
		return nil, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsAuthenticateSessionOperation(), contracts.AdminAuthenticateSessionRequest{Token: token})
	if err != nil {
		return nil, err
	}
	return contracts.WithAdminSubject(ctx, response.Subject), nil
}

func (adapter *adminFormsAdapter) ListSchemas(ctx context.Context, cursor string, limit int) (contracts.AdminFormsSchemaPage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFormsSchemaPage{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminFormsSchemaPage{}, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsListSchemasOperation(), contracts.AdminFormsSchemaListRequest{Subject: subject, Cursor: cursor, Limit: limit})
	if err != nil {
		return contracts.AdminFormsSchemaPage{}, err
	}
	return response.Page, nil
}

func (adapter *adminFormsAdapter) GetSchema(ctx context.Context, id string) (contracts.AdminFormsSchema, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFormsSchema{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminFormsSchema{}, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsGetSchemaOperation(), contracts.AdminFormsSchemaIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminFormsSchema{}, err
	}
	return response.Schema, nil
}

func (adapter *adminFormsAdapter) SaveSchema(ctx context.Context, request contracts.AdminFormsSaveSchemaRequest) (contracts.AdminFormsSchema, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFormsSchema{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminFormsSchema{}, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsSaveSchemaOperation(), contracts.AdminFormsSaveSchemaRequestMessage{Subject: subject, Request: request})
	if err != nil {
		return contracts.AdminFormsSchema{}, err
	}
	return response.Schema, nil
}

func (adapter *adminFormsAdapter) ListSubmissions(ctx context.Context, request contracts.AdminFormsListSubmissionsRequest) (contracts.AdminFormsSubmissionPage, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFormsSubmissionPage{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminFormsSubmissionPage{}, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsListSubmissionsOperation(), contracts.AdminFormsListSubmissionsRequestMessage{Subject: subject, Request: request})
	if err != nil {
		return contracts.AdminFormsSubmissionPage{}, err
	}
	return response.Page, nil
}

func (adapter *adminFormsAdapter) GetSubmission(ctx context.Context, id string) (contracts.AdminFormsSubmission, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFormsSubmission{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminFormsSubmission{}, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsGetSubmissionOperation(), contracts.AdminFormsSchemaIDRequest{Subject: subject, ID: id})
	if err != nil {
		return contracts.AdminFormsSubmission{}, err
	}
	return response.Submission, nil
}

func (adapter *adminFormsAdapter) SetSubmissionStatus(ctx context.Context, request contracts.AdminFormsSetSubmissionStatusRequest) (contracts.AdminFormsSubmission, error) {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return contracts.AdminFormsSubmission{}, err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.AdminFormsSubmission{}, contracts.ErrAdminFormsUnavailable
	}
	response, err := contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsSetSubmissionStatusOperation(), contracts.AdminFormsSetSubmissionStatusRequestMessage{Subject: subject, Request: request})
	if err != nil {
		return contracts.AdminFormsSubmission{}, err
	}
	return response.Submission, nil
}

func (adapter *adminFormsAdapter) DeleteSubmission(ctx context.Context, request contracts.AdminFormsDeleteSubmissionRequest) error {
	subject, err := adapter.subject(ctx)
	if err != nil {
		return err
	}
	if adapter == nil || adapter.dispatcher == nil {
		return contracts.ErrAdminFormsUnavailable
	}
	_, err = contracts.Call(ctx, adapter.dispatcher, contracts.AdminFormsDeleteSubmissionOperation(), contracts.AdminFormsDeleteSubmissionRequestMessage{Subject: subject, Request: request})
	return err
}

var _ contracts.AdminFormsAPI = (*adminFormsAdapter)(nil)
