import * as React from 'react';
import Helmet from 'react-helmet';
import {
  PageSection,
  Title,
  Button,
  Card,
  CardBody,
  CardHeader,
  CardTitle,
  EmptyState,
  EmptyStateBody,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalVariant,
  FormGroup,
  FormHelperText,
  HelperText,
  HelperTextItem,
  TextInput,
  Label,
  Split,
  SplitItem,
  DescriptionList,
  DescriptionListGroup,
  DescriptionListTerm,
  DescriptionListDescription,
  Alert,
} from '@patternfly/react-core';
import {
  listEndpoints,
  createEndpoint,
  updateEndpoint,
  deleteEndpoint,
  healthCheckEndpoint,
  MaaSEndpoint,
} from '../utils/api';

export default function SettingsPage() {
  const [endpoints, setEndpoints] = React.useState<MaaSEndpoint[]>([]);
  const [showCreate, setShowCreate] = React.useState(false);
  const [showEdit, setShowEdit] = React.useState(false);
  const [editId, setEditId] = React.useState<number | null>(null);
  const [name, setName] = React.useState('');
  const [url, setUrl] = React.useState('');
  const [apiKey, setApiKey] = React.useState('');
  const [providerType, setProviderType] = React.useState('openai-compatible');
  const [healthStatus, setHealthStatus] = React.useState<Record<number, { healthy: boolean; error?: string } | null>>({});

  React.useEffect(() => { loadEndpoints(); }, []);

  const loadEndpoints = async () => {
    try { setEndpoints(await listEndpoints() || []); } catch (e) { console.error(e); }
  };

  const handleCreate = async () => {
    try {
      await createEndpoint({ name, url, api_key: apiKey, provider_type: providerType });
      setShowCreate(false);
      resetForm();
      loadEndpoints();
    } catch (e) { console.error(e); }
  };

  const handleEdit = async () => {
    if (editId === null) return;
    try {
      await updateEndpoint(editId, { name, url, api_key: apiKey, provider_type: providerType } as any);
      setShowEdit(false);
      resetForm();
      loadEndpoints();
    } catch (e) { console.error(e); }
  };

  const handleDelete = async (id: number) => {
    try { await deleteEndpoint(id); loadEndpoints(); } catch (e) { console.error(e); }
  };

  const handleHealthCheck = async (id: number) => {
    try {
      const result = await healthCheckEndpoint(id);
      setHealthStatus(prev => ({ ...prev, [id]: result }));
    } catch (e: any) {
      setHealthStatus(prev => ({ ...prev, [id]: { healthy: false, error: e.message } }));
    }
  };

  const openEdit = (ep: MaaSEndpoint) => {
    setEditId(ep.id);
    setName(ep.name);
    setUrl(ep.url);
    setApiKey('');
    setProviderType(ep.provider_type);
    setShowEdit(true);
  };

  const resetForm = () => {
    setName(''); setUrl(''); setApiKey(''); setProviderType('openai-compatible'); setEditId(null);
  };

  return (
    <>
      <Helmet><title>Skills Settings</title></Helmet>
        <PageSection>
          <Split hasGutter>
            <SplitItem isFilled><Title headingLevel="h1">MaaS Endpoints</Title></SplitItem>
            <SplitItem>
              <Button variant="primary" onClick={() => { resetForm(); setShowCreate(true); }}>
                Add Endpoint
              </Button>
            </SplitItem>
          </Split>
        </PageSection>
        <PageSection>
          {endpoints.length === 0 ? (
            <EmptyState>
              <Title headingLevel="h2" size="lg">No endpoints configured</Title>
              <EmptyStateBody>
                Add an OpenAI-compatible Model-as-a-Service endpoint to get started (e.g. OpenShift AI, vLLM, Ollama).
              </EmptyStateBody>
            </EmptyState>
          ) : (
            endpoints.map(ep => (
              <Card key={ep.id} isCompact className="pf-v6-u-mb-md">
                <CardHeader
                  actions={{
                    actions: (
                      <>
                        <Button variant="link" onClick={() => handleHealthCheck(ep.id)}>Health Check</Button>
                        <Button variant="link" onClick={() => openEdit(ep)}>Edit</Button>
                        <Button variant="link" isDanger onClick={() => handleDelete(ep.id)}>Delete</Button>
                      </>
                    ),
                  }}
                >
                  <CardTitle>
                    {ep.name}{' '}
                    <Label color={ep.enabled ? 'green' : 'grey'}>{ep.provider_type}</Label>
                  </CardTitle>
                </CardHeader>
                <CardBody>
                  <DescriptionList isHorizontal isCompact>
                    <DescriptionListGroup>
                      <DescriptionListTerm>URL</DescriptionListTerm>
                      <DescriptionListDescription>{ep.url}</DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>API Key</DescriptionListTerm>
                      <DescriptionListDescription>{ep.api_key ? 'Configured' : 'Not set'}</DescriptionListDescription>
                    </DescriptionListGroup>
                  </DescriptionList>
                  {healthStatus[ep.id] !== undefined && healthStatus[ep.id] !== null && (
                    <Alert
                      variant={healthStatus[ep.id].healthy ? 'success' : 'danger'}
                      title={healthStatus[ep.id].healthy ? 'Endpoint is healthy' : 'Endpoint unreachable'}
                      isInline
                      className="pf-v6-u-mt-sm"
                    >
                      {healthStatus[ep.id].error}
                    </Alert>
                  )}
                </CardBody>
              </Card>
            ))
          )}
        </PageSection>

      {/* Create modal */}
      <Modal
        variant={ModalVariant.small}
        isOpen={showCreate}
        onClose={() => setShowCreate(false)}
      >
        <ModalHeader title="Add MaaS Endpoint" />
        <ModalBody>
          <FormGroup label="Name" isRequired fieldId="ep-name">
            <TextInput id="ep-name" value={name} onChange={(_e, v) => setName(v)} placeholder="My MaaS Endpoint" />
          </FormGroup>
          <FormGroup label="URL" isRequired fieldId="ep-url">
            <TextInput id="ep-url" value={url} onChange={(_e, v) => setUrl(v)} placeholder="https://model-server.example.com/v1" />
            <FormHelperText>
              <HelperText>
                <HelperTextItem>OpenAI-compatible /v1 base URL</HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup label="API Key" fieldId="ep-key">
            <TextInput id="ep-key" type="password" value={apiKey} onChange={(_e, v) => setApiKey(v)} />
          </FormGroup>
          <FormGroup label="Provider Type" fieldId="ep-type">
            <TextInput id="ep-type" value={providerType} onChange={(_e, v) => setProviderType(v)} />
          </FormGroup>
        </ModalBody>
        <ModalFooter>
          <Button variant="primary" onClick={handleCreate} isDisabled={!name || !url}>Create</Button>
          <Button variant="link" onClick={() => setShowCreate(false)}>Cancel</Button>
        </ModalFooter>
      </Modal>

      {/* Edit modal */}
      <Modal
        variant={ModalVariant.small}
        isOpen={showEdit}
        onClose={() => setShowEdit(false)}
      >
        <ModalHeader title="Edit MaaS Endpoint" />
        <ModalBody>
          <FormGroup label="Name" isRequired fieldId="edit-ep-name">
            <TextInput id="edit-ep-name" value={name} onChange={(_e, v) => setName(v)} />
          </FormGroup>
          <FormGroup label="URL" isRequired fieldId="edit-ep-url">
            <TextInput id="edit-ep-url" value={url} onChange={(_e, v) => setUrl(v)} />
          </FormGroup>
          <FormGroup label="API Key" fieldId="edit-ep-key">
            <TextInput id="edit-ep-key" type="password" value={apiKey} onChange={(_e, v) => setApiKey(v)} />
            <FormHelperText>
              <HelperText>
                <HelperTextItem>Leave blank to keep existing key</HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup label="Provider Type" fieldId="edit-ep-type">
            <TextInput id="edit-ep-type" value={providerType} onChange={(_e, v) => setProviderType(v)} />
          </FormGroup>
        </ModalBody>
        <ModalFooter>
          <Button variant="primary" onClick={handleEdit}>Save</Button>
          <Button variant="link" onClick={() => setShowEdit(false)}>Cancel</Button>
        </ModalFooter>
      </Modal>
    </>
  );
}
