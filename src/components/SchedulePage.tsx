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
  FormSelect,
  FormSelectOption,
  Label,
  Split,
  SplitItem,
  Switch,
  DescriptionList,
  DescriptionListGroup,
  DescriptionListTerm,
  DescriptionListDescription,
  ExpandableSection,
} from '@patternfly/react-core';
import {
  listScheduledTasks,
  createScheduledTask,
  updateScheduledTask,
  deleteScheduledTask,
  toggleScheduledTask,
  getTaskHistory,
  listSkills,
  listEndpoints,
  listModels,
  getHealth,
  ScheduledTask,
  TaskHistory,
  Skill,
  MaaSEndpoint,
  ModelInfo,
} from '../utils/api';

export default function SchedulePage() {
  const [tasks, setTasks] = React.useState<ScheduledTask[]>([]);
  const [skills, setSkills] = React.useState<Skill[]>([]);
  const [endpoints, setEndpoints] = React.useState<MaaSEndpoint[]>([]);
  const [models, setModels] = React.useState<ModelInfo[]>([]);
  const [showCreate, setShowCreate] = React.useState(false);
  const [editingTask, setEditingTask] = React.useState<ScheduledTask | null>(null);
  const [historyMap, setHistoryMap] = React.useState<Record<number, TaskHistory[]>>({});
  const [expandedTasks, setExpandedTasks] = React.useState<Record<number, boolean>>({});

  // Form state
  const [name, setName] = React.useState('');
  const [description, setDescription] = React.useState('');
  const [schedule, setSchedule] = React.useState('');
  const [selectedSkillId, setSelectedSkillId] = React.useState('');
  const [pluginNamespace, setPluginNamespace] = React.useState('default');
  const [serviceAccount, setServiceAccount] = React.useState('default');
  const [namespace, setNamespace] = React.useState('default');
  const [containerImage, setContainerImage] = React.useState('');
  const [temperature, setTemperature] = React.useState(0.2);
  const [maxTokens, setMaxTokens] = React.useState(2048);
  const [runOnce, setRunOnce] = React.useState(false);
  const [runOnceDelay, setRunOnceDelay] = React.useState('now');
  const [selectedEndpoint, setSelectedEndpoint] = React.useState('');
  const [selectedModelId, setSelectedModelId] = React.useState('');

  React.useEffect(() => {
    loadTasks();
    loadSkills();
    loadEndpoints();
    getHealth().then(h => {
      setPluginNamespace(h.namespace);
      setNamespace(h.namespace);
    }).catch(() => {});
  }, []);

  const loadTasks = async () => {
    try { setTasks(await listScheduledTasks() || []); } catch (e) { console.error(e); }
  };
  const loadSkills = async () => {
    try { setSkills(await listSkills() || []); } catch (e) { console.error(e); }
  };
  const loadEndpoints = async () => {
    try { setEndpoints(await listEndpoints() || []); } catch (e) { console.error(e); }
  };
  const loadModelsForEndpoint = async (endpointId: string, preserveModelId?: string) => {
    try {
      const data = await listModels(endpointId ? parseInt(endpointId) : undefined);
      setModels(data || []);
      if (preserveModelId && data?.some(m => m.id === preserveModelId)) {
        setSelectedModelId(preserveModelId);
      } else if (data && data.length > 0) {
        setSelectedModelId(data[0].id);
      }
    } catch (e) { setModels([]); }
  };

  const loadHistory = async (taskId: number) => {
    try {
      const h = await getTaskHistory(taskId);
      setHistoryMap(prev => ({ ...prev, [taskId]: h || [] }));
    } catch (e) { console.error(e); }
  };

  const toggleExpanded = (taskId: number) => {
    const newVal = !expandedTasks[taskId];
    setExpandedTasks(prev => ({ ...prev, [taskId]: newVal }));
    if (newVal) loadHistory(taskId);
  };

  const handleCreate = async () => {
    const selectedModel = models.find(m => m.id === selectedModelId);
    try {
      await createScheduledTask({
        name,
        description,
        schedule: runOnce ? '' : schedule,
        skill_id: selectedSkillId ? parseInt(selectedSkillId) : undefined,
        service_account: serviceAccount,
        namespace,
        provider: 'openai-compatible',
        model: selectedModelId,
        base_url: selectedModel?.url,
        container_image: containerImage,
        temperature,
        max_tokens: maxTokens,
        run_once: runOnce,
        run_once_delay: runOnce ? runOnceDelay : undefined,
      });
      setShowCreate(false);
      resetForm();
      loadTasks();
    } catch (e) { console.error(e); }
  };

  const handleEdit = (task: ScheduledTask) => {
    setEditingTask(task);
    setName(task.name);
    setDescription(task.description || '');
    setSchedule(task.schedule);
    setSelectedSkillId(task.skill_id ? task.skill_id.toString() : '');
    setServiceAccount(task.service_account);
    setNamespace(task.namespace);
    setContainerImage(task.container_image || '');
    setTemperature(task.temperature || 0.2);
    setMaxTokens(task.max_tokens || 2048);
    setRunOnce(task.run_once || false);
    setRunOnceDelay(task.run_once_delay || 'now');
    setSelectedModelId(task.model);
    // Find the endpoint that matches this task's base_url and load its models
    const matchingEndpoint = endpoints.find(e => task.base_url && task.base_url.startsWith(e.url));
    const epId = matchingEndpoint ? matchingEndpoint.id.toString() : (endpoints.length > 0 ? endpoints[0].id.toString() : '');
    setSelectedEndpoint(epId);
    if (epId) {
      loadModelsForEndpoint(epId, task.model);
    }
    setShowCreate(true);
  };

  const handleUpdate = async () => {
    if (!editingTask) return;
    const selectedModel = models.find(m => m.id === selectedModelId);
    try {
      await updateScheduledTask(editingTask.id, {
        name,
        description,
        schedule: runOnce ? '' : schedule,
        skill_id: selectedSkillId ? parseInt(selectedSkillId) : undefined,
        service_account: serviceAccount,
        namespace,
        provider: 'openai-compatible',
        model: selectedModelId,
        base_url: selectedModel?.url || editingTask.base_url,
        container_image: containerImage,
        temperature,
        max_tokens: maxTokens,
        run_once: runOnce,
        run_once_delay: runOnce ? runOnceDelay : undefined,
      });
      setShowCreate(false);
      setEditingTask(null);
      resetForm();
      loadTasks();
    } catch (e) { console.error(e); }
  };

  const handleToggle = async (task: ScheduledTask) => {
    try {
      await toggleScheduledTask(task.id, !task.enabled);
      loadTasks();
    } catch (e) { console.error(e); }
  };

  const handleDelete = async (id: number) => {
    try { await deleteScheduledTask(id); loadTasks(); } catch (e) { console.error(e); }
  };

  const resetForm = () => {
    setName(''); setDescription(''); setSchedule(''); setSelectedSkillId('');
    setServiceAccount('default'); setNamespace(pluginNamespace); setContainerImage('');
    setTemperature(0.2); setMaxTokens(2048); setRunOnce(false); setRunOnceDelay('now');
    setSelectedEndpoint(''); setSelectedModelId('');
    setEditingTask(null);
  };

  const statusColor = (s: string) => {
    if (s === 'success') return 'green';
    if (s === 'failed') return 'red';
    return 'blue';
  };

  return (
    <>
      <Helmet><title>Skills Schedule</title></Helmet>
        <PageSection>
          <Split hasGutter>
            <SplitItem isFilled><Title headingLevel="h1">Scheduled Skills</Title></SplitItem>
            <SplitItem>
              <Button variant="primary" onClick={() => { resetForm(); setShowCreate(true); }}>
                New Scheduled Task
              </Button>
            </SplitItem>
          </Split>
        </PageSection>
        <PageSection>
          {tasks.length === 0 ? (
            <EmptyState>
              <Title headingLevel="h2" size="lg">No scheduled tasks</Title>
              <EmptyStateBody>Create a scheduled task to run skills on a cron schedule.</EmptyStateBody>
            </EmptyState>
          ) : (
            tasks.map(t => (
              <Card key={t.id} isCompact className="pf-v6-u-mb-md">
                <CardHeader
                  actions={{
                    actions: (
                      <>
                        <Button variant="link" onClick={() => handleEdit(t)}>Edit</Button>
                        <Switch
                          id={'toggle-task-' + t.id}
                          isChecked={t.enabled}
                          onChange={() => handleToggle(t)}
                          aria-label="Enable task"
                        />
                        <Button variant="link" isDanger onClick={() => handleDelete(t.id)}>Delete</Button>
                      </>
                    ),
                  }}
                >
                  <CardTitle>
                    {t.name}{' '}
                    <Label color={t.enabled ? 'green' : 'grey'}>{t.enabled ? 'Active' : 'Paused'}</Label>
                  </CardTitle>
                </CardHeader>
                <CardBody>
                  <DescriptionList isHorizontal isCompact>
                    <DescriptionListGroup>
                      <DescriptionListTerm>Schedule</DescriptionListTerm>
                      <DescriptionListDescription>
                        {t.run_once ? (
                          <><Label color="purple" isCompact>Run Once</Label>{' '}{t.run_once_delay || 'now'}</>
                        ) : (
                          <code>{t.schedule}</code>
                        )}
                      </DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>ServiceAccount</DescriptionListTerm>
                      <DescriptionListDescription>{t.service_account} / {t.namespace}</DescriptionListDescription>
                    </DescriptionListGroup>
                    <DescriptionListGroup>
                      <DescriptionListTerm>Model</DescriptionListTerm>
                      <DescriptionListDescription>{t.model} ({t.provider})</DescriptionListDescription>
                    </DescriptionListGroup>
                    {t.container_image && (
                      <DescriptionListGroup>
                        <DescriptionListTerm>Container Image</DescriptionListTerm>
                        <DescriptionListDescription>{t.container_image}</DescriptionListDescription>
                      </DescriptionListGroup>
                    )}
                    <DescriptionListGroup>
                      <DescriptionListTerm>Runs</DescriptionListTerm>
                      <DescriptionListDescription>{t.run_count}</DescriptionListDescription>
                    </DescriptionListGroup>
                    {t.last_run && (
                      <DescriptionListGroup>
                        <DescriptionListTerm>Last Run</DescriptionListTerm>
                        <DescriptionListDescription>{new Date(t.last_run).toLocaleString()}</DescriptionListDescription>
                      </DescriptionListGroup>
                    )}
                  </DescriptionList>

                  <ExpandableSection
                    toggleText={expandedTasks[t.id] ? 'Hide execution history' : 'Show execution history'}
                    isExpanded={!!expandedTasks[t.id]}
                    onToggle={() => toggleExpanded(t.id)}
                  >
                    {(historyMap[t.id] || []).length === 0 ? (
                      <p>No executions yet.</p>
                    ) : (
                      (historyMap[t.id] || []).map(h => (
                        <Card key={h.id} isCompact isPlain className="pf-v6-u-mb-sm">
                          <CardBody>
                            <Split hasGutter>
                              <SplitItem><Label color={statusColor(h.status)}>{h.status}</Label></SplitItem>
                              <SplitItem>{new Date(h.started_at).toLocaleString()}</SplitItem>
                              {h.duration_ms !== undefined && <SplitItem>{h.duration_ms}ms</SplitItem>}
                            </Split>
                            {h.output && <pre className="task-output">{h.output}</pre>}
                            {h.error_message && <pre className="task-error">{h.error_message}</pre>}
                          </CardBody>
                        </Card>
                      ))
                    )}
                  </ExpandableSection>
                </CardBody>
              </Card>
            ))
          )}
        </PageSection>

      <Modal
        variant={ModalVariant.medium}
        isOpen={showCreate}
        onClose={() => { setShowCreate(false); setEditingTask(null); }}
      >
        <ModalHeader title={editingTask ? 'Edit Scheduled Task' : 'New Scheduled Task'} />
        <ModalBody>
          <FormGroup label="Name" isRequired fieldId="task-name">
            <TextInput id="task-name" value={name} onChange={(_e, v) => setName(v)} />
          </FormGroup>
          <FormGroup label="Prompt" fieldId="task-desc">
            <TextArea id="task-desc" value={description} onChange={(_e, v) => setDescription(v)} rows={3} placeholder="Instructions to send with the skill to the agent..." />
          </FormGroup>
          <FormGroup fieldId="task-run-once">
            <Switch
              id="task-run-once"
              label={runOnce ? 'Run Once' : 'Recurring (Cron)'}
              isChecked={runOnce}
              onChange={(_e, checked) => setRunOnce(checked)}
            />
          </FormGroup>
          {runOnce ? (
            <FormGroup label="Run Delay" isRequired fieldId="task-delay">
              <TextInput id="task-delay" value={runOnceDelay} onChange={(_e, v) => setRunOnceDelay(v)} placeholder="now" />
              <FormHelperText>
                <HelperText>
                  <HelperTextItem>&quot;now&quot; for immediate, or delay like &quot;+30s&quot;, &quot;+5m&quot;, &quot;+2h&quot;, &quot;+1h30m&quot;</HelperTextItem>
                </HelperText>
              </FormHelperText>
            </FormGroup>
          ) : (
            <FormGroup label="Cron Schedule" isRequired fieldId="task-schedule">
              <TextInput id="task-schedule" value={schedule} onChange={(_e, v) => setSchedule(v)} placeholder="0 9 * * *" />
              <FormHelperText>
                <HelperText>
                  <HelperTextItem>e.g. */5 * * * * (every 5 min)</HelperTextItem>
                </HelperText>
              </FormHelperText>
            </FormGroup>
          )}
          <FormGroup label="Skill" fieldId="task-skill">
            <FormSelect id="task-skill" value={selectedSkillId} onChange={(_e, val) => setSelectedSkillId(val)}>
              <FormSelectOption value="" label="-- None (manual prompt) --" />
              {skills.filter(s => s.enabled).map(s => (
                <FormSelectOption key={s.id} value={s.id.toString()} label={s.name} />
              ))}
            </FormSelect>
          </FormGroup>
          <FormGroup label="ServiceAccount" isRequired fieldId="task-sa">
            <TextInput id="task-sa" value={serviceAccount} onChange={(_e, v) => setServiceAccount(v)} />
          </FormGroup>
          <FormGroup label="Namespace" isRequired fieldId="task-ns">
            <TextInput id="task-ns" value={namespace} onChange={(_e, v) => setNamespace(v)} />
          </FormGroup>
          <FormGroup label="Container Image" fieldId="task-image">
            <TextInput id="task-image" value={containerImage} onChange={(_e, v) => setContainerImage(v)} placeholder="quay.io/org/image:tag" />
            <FormHelperText>
              <HelperText>
                <HelperTextItem>Container image to use for the CronJob execution</HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup label="MaaS Endpoint" fieldId="task-endpoint">
            <FormSelect
              id="task-endpoint"
              value={selectedEndpoint}
              onChange={(_e, val) => { setSelectedEndpoint(val); loadModelsForEndpoint(val); }}
            >
              <FormSelectOption value="" label="-- Use default --" />
              {endpoints.map(e => (
                <FormSelectOption key={e.id} value={e.id.toString()} label={e.name} />
              ))}
            </FormSelect>
          </FormGroup>
          <FormGroup label="Model" fieldId="task-model">
            <FormSelect id="task-model" value={selectedModelId} onChange={(_e, val) => setSelectedModelId(val)}>
              {models.length === 0 && <FormSelectOption value="" label="-- Select endpoint first --" />}
              {models.map(m => (
                <FormSelectOption key={m.id} value={m.id} label={m.display_name + (m.ready ? '' : ' (not ready)')} />
              ))}
            </FormSelect>
          </FormGroup>
          <FormGroup label="Temperature" fieldId="task-temperature">
            <TextInput id="task-temperature" type="number" value={temperature.toString()} onChange={(_e, v) => setTemperature(parseFloat(v) || 0)} />
            <FormHelperText>
              <HelperText>
                <HelperTextItem>Controls randomness (0.0 = deterministic, 1.0+ = creative). Default: 0.7</HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup label="Max Token Length" fieldId="task-max-tokens">
            <TextInput id="task-max-tokens" type="number" value={maxTokens.toString()} onChange={(_e, v) => setMaxTokens(parseInt(v) || 0)} />
            <FormHelperText>
              <HelperText>
                <HelperTextItem>Maximum tokens in the response (0 = model default)</HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
        </ModalBody>
        <ModalFooter>
          <Button variant="primary" onClick={editingTask ? handleUpdate : handleCreate} isDisabled={!name || (!runOnce && !schedule)}>
            {editingTask ? 'Save' : 'Create'}
          </Button>
          <Button variant="link" onClick={() => { setShowCreate(false); setEditingTask(null); }}>Cancel</Button>
        </ModalFooter>
      </Modal>
    </>
  );
}
