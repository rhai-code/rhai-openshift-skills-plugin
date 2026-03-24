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
  TextArea,
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
  exportDatabase,
  importDatabase,
  getConfig,
  setConfig,
  MaaSEndpoint,
} from '../utils/api';
import { useAuth } from '../utils/AuthContext';

export default function SettingsPage() {
  const { username, isAdmin } = useAuth();
  const [endpoints, setEndpoints] = React.useState<MaaSEndpoint[]>([]);
  const [showCreate, setShowCreate] = React.useState(false);
  const [showEdit, setShowEdit] = React.useState(false);
  const [editId, setEditId] = React.useState<number | null>(null);
  const [name, setName] = React.useState('');
  const [url, setUrl] = React.useState('');
  const [apiKey, setApiKey] = React.useState('');
  const [providerType, setProviderType] = React.useState('openai-compatible');
  const [healthStatus, setHealthStatus] = React.useState<Record<number, { healthy: boolean; error?: string } | null>>({});
  const [dbMessage, setDbMessage] = React.useState<{ type: 'success' | 'danger'; text: string } | null>(null);
  const [importing, setImporting] = React.useState(false);
  const fileInputRef = React.useRef<HTMLInputElement>(null);
  const [systemPrompt, setSystemPrompt] = React.useState('');
  const [systemPromptSaved, setSystemPromptSaved] = React.useState(false);

  React.useEffect(() => {
    loadEndpoints();
    loadSystemPrompt();
  }, []);

  const loadEndpoints = async () => {
    try { setEndpoints(await listEndpoints() || []); } catch (e) { console.error(e); }
  };

  const loadSystemPrompt = async () => {
    try {
      const result = await getConfig('system_prompt');
      setSystemPrompt(result.value || '');
    } catch (e) { console.error(e); }
  };

  const handleSaveSystemPrompt = async () => {
    try {
      await setConfig('system_prompt', systemPrompt);
      setSystemPromptSaved(true);
      setTimeout(() => setSystemPromptSaved(false), 3000);
    } catch (e) { console.error(e); }
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
      const result = await healthCheckEndpoint(id) as any;
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

  const handleExport = async () => {
    try {
      setDbMessage(null);
      await exportDatabase();
      setDbMessage({ type: 'success', text: 'Database exported successfully.' });
    } catch (e: any) {
      setDbMessage({ type: 'danger', text: 'Export failed: ' + e.message });
    }
  };

  const handleImport = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    setImporting(true);
    setDbMessage(null);
    try {
      const result = await importDatabase(file);
      setDbMessage({ type: 'success', text: result.message });
      loadEndpoints();
    } catch (e: any) {
      setDbMessage({ type: 'danger', text: 'Import failed: ' + e.message });
    }
    setImporting(false);
    if (fileInputRef.current) fileInputRef.current.value = '';
  };

  const resetForm = () => {
    setName(''); setUrl(''); setApiKey(''); setProviderType('openai-compatible'); setEditId(null);
  };

  return (
    <>
      <Helmet><title>Skills Settings</title></Helmet>

        <PageSection>
          <Title headingLevel="h1">System Prompt</Title>
        </PageSection>
        <PageSection>
          <Card isCompact>
            <CardBody>
              <FormGroup label="Agent Instructions (always prepended, read-only)" fieldId="agent-instructions">
                <TextArea
                  id="agent-instructions"
                  value={`You are an AI agent running on an OpenShift cluster.\nYou have access to the 'shell' tool to execute commands.\nUse 'oc' and 'kubectl' commands to interact with the cluster.\nExecute commands to get real data - do NOT fabricate or hallucinate results.\nOnly report what the commands actually return.\nIMPORTANT: For multi-line scripts or commands containing quotes, write the script to a temp file first using a heredoc, then execute it.`}
                  rows={6}
                  isDisabled
                  aria-label="Agent instructions"
                />
              </FormGroup>
              <FormGroup label="Custom System Prompt" fieldId="system-prompt" className="pf-v6-u-mt-md">
                <TextArea
                  id="system-prompt"
                  value={systemPrompt}
                  onChange={(_e, v) => { setSystemPrompt(v); setSystemPromptSaved(false); }}
                  rows={4}
                  placeholder="Add custom instructions here..."
                  aria-label="System prompt"
                  isDisabled={!isAdmin}
                />
                <FormHelperText>
                  <HelperText>
                    <HelperTextItem>
                      Applied to all new chat sessions and scheduled tasks. Appended after agent instructions, before skills.
                    </HelperTextItem>
                  </HelperText>
                </FormHelperText>
              </FormGroup>
              <Split hasGutter className="pf-v6-u-mt-sm">
                {isAdmin ? (
                  <SplitItem>
                    <Button variant="primary" onClick={handleSaveSystemPrompt}>Save</Button>
                  </SplitItem>
                ) : (
                  <SplitItem>
                    <Alert variant="info" title="Admin access required to modify system prompt" isInline isPlain />
                  </SplitItem>
                )}
                {systemPromptSaved && (
                  <SplitItem>
                    <Alert variant="success" title="System prompt saved" isInline isPlain />
                  </SplitItem>
                )}
              </Split>
            </CardBody>
          </Card>
        </PageSection>

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
                        {(isAdmin || ep.owner === username) && (
                          <>
                            <Button variant="link" onClick={() => openEdit(ep)}>Edit</Button>
                            <Button variant="link" isDanger onClick={() => handleDelete(ep.id)}>Delete</Button>
                          </>
                        )}
                      </>
                    ),
                  }}
                >
                  <CardTitle>
                    {ep.name}{' '}
                    <Label color={ep.enabled ? 'green' : 'grey'}>{ep.provider_type}</Label>{' '}
                    <Label color={ep.is_global ? 'blue' : 'orange'}>{ep.is_global ? 'Global' : 'Private'}</Label>
                    {ep.owner && <>{' '}<Label color="grey">{ep.owner}</Label></>}
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
                    {ep.single_model && (
                      <DescriptionListGroup>
                        <DescriptionListTerm>Type</DescriptionListTerm>
                        <DescriptionListDescription>
                          <Label color="blue">Single model: {ep.model_name}</Label>
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                    )}
                  </DescriptionList>
                  {healthStatus[ep.id] !== undefined && healthStatus[ep.id] !== null && (
                    <Alert
                      variant={healthStatus[ep.id].healthy ? 'success' : 'danger'}
                      title={healthStatus[ep.id].healthy
                        ? ((healthStatus[ep.id] as any).single_model
                          ? `Single OpenAI-compatible model endpoint OK (model: ${(healthStatus[ep.id] as any).model_name})`
                          : 'Endpoint is healthy')
                        : 'Endpoint unreachable'}
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

        {isAdmin && (
          <>
            <PageSection>
              <Title headingLevel="h1">Database</Title>
            </PageSection>
            <PageSection>
              <Card isCompact>
                <CardBody>
                  <Split hasGutter>
                    <SplitItem>
                      <Button variant="secondary" onClick={handleExport}>Export Database</Button>
                    </SplitItem>
                    <SplitItem>
                      <input
                        type="file"
                        ref={fileInputRef}
                        accept=".db,.sqlite,.sqlite3"
                        style={{ display: 'none' }}
                        onChange={handleImport}
                      />
                      <Button variant="secondary" onClick={() => fileInputRef.current?.click()} isLoading={importing} isDisabled={importing}>
                        Import Database
                      </Button>
                    </SplitItem>
                  </Split>
                  {dbMessage && (
                    <Alert variant={dbMessage.type} title={dbMessage.text} isInline className="pf-v6-u-mt-sm" />
                  )}
                </CardBody>
              </Card>
            </PageSection>
          </>
        )}

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
            <TextInput id="ep-url" value={url} onChange={(_e, v) => setUrl(v)} placeholder="https://maas.example.com/maas-api or https://model.example.com/v1" />
            <FormHelperText>
              <HelperText>
                <HelperTextItem>MaaS API or OpenAI-compatible /v1 base URL</HelperTextItem>
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
