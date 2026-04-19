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
  TextInput,
  TextArea,
  Label,
  Split,
  SplitItem,
  Gallery,
  GalleryItem,
  Switch,
  FileUpload,
} from '@patternfly/react-core';
import {
  listSkills,
  createSkill,
  updateSkill,
  deleteSkill,
  uploadSkill,
  Skill,
} from '../utils/api';
import { useAuth } from '../utils/AuthContext';

export default function SkillsPage() {
  const { username, isAdmin } = useAuth();
  const [skills, setSkills] = React.useState<Skill[]>([]);
  const [showCreate, setShowCreate] = React.useState(false);
  const [showUpload, setShowUpload] = React.useState(false);
  const [showEdit, setShowEdit] = React.useState(false);
  const [editSkill, setEditSkill] = React.useState<Skill | null>(null);
  const [name, setName] = React.useState('');
  const [description, setDescription] = React.useState('');
  const [content, setContent] = React.useState('');
  const [uploadFile, setUploadFile] = React.useState<File | null>(null);
  const [uploadFilename, setUploadFilename] = React.useState('');

  const canEdit = (s: Skill) => isAdmin || s.owner === username;
  const canToggleGlobal = (s: Skill) => isAdmin || s.owner === username;

  React.useEffect(() => { loadSkills(); }, []);

  const loadSkills = async () => {
    try {
      const data = await listSkills();
      setSkills(data || []);
    } catch (e) {
      console.error('Failed to load skills', e);
    }
  };

  const handleCreate = async () => {
    try {
      await createSkill({ name, description, content });
      setShowCreate(false);
      resetForm();
      loadSkills();
    } catch (e) {
      console.error('Failed to create skill', e);
    }
  };

  const handleUpload = async () => {
    if (!uploadFile) return;
    try {
      await uploadSkill(uploadFile, name || undefined, description || undefined);
      setShowUpload(false);
      resetForm();
      loadSkills();
    } catch (e) {
      console.error('Failed to upload skill', e);
    }
  };

  const handleEdit = async () => {
    if (!editSkill) return;
    try {
      await updateSkill(editSkill.id, { name, description, content });
      setShowEdit(false);
      resetForm();
      loadSkills();
    } catch (e) {
      console.error('Failed to update skill', e);
    }
  };

  const handleDelete = async (id: number) => {
    try {
      await deleteSkill(id);
      loadSkills();
    } catch (e) {
      console.error('Failed to delete skill', e);
    }
  };

  const handleToggle = async (skill: Skill) => {
    try {
      await updateSkill(skill.id, { enabled: !skill.enabled });
      loadSkills();
    } catch (e) {
      console.error('Failed to toggle skill', e);
    }
  };

  const openEdit = (skill: Skill) => {
    setEditSkill(skill);
    setName(skill.name);
    setDescription(skill.description);
    setContent(skill.content);
    setShowEdit(true);
  };

  const resetForm = () => {
    setName('');
    setDescription('');
    setContent('');
    setUploadFile(null);
    setUploadFilename('');
    setEditSkill(null);
  };

  const handleFileChange = (_e: React.FormEvent<HTMLDivElement>) => {
    // PF6 FileUpload onChange fires on the wrapper div;
    // actual file data comes via onFileInputChange
  };

  const handleFileInputChange = (_e: React.ChangeEvent<HTMLInputElement>, file: File) => {
    setUploadFile(file);
    setUploadFilename(file.name);
  };

  return (
    <>
      <Helmet><title>Skills Manager</title></Helmet>
        <PageSection>
          <Split hasGutter>
            <SplitItem isFilled>
              <Title headingLevel="h1">Skills Manager</Title>
            </SplitItem>
            <SplitItem>
              <Button variant="primary" onClick={() => { resetForm(); setShowCreate(true); }}>Create Skill</Button>
              {' '}
              <Button variant="secondary" onClick={() => { resetForm(); setShowUpload(true); }}>Upload SKILLS.md</Button>
            </SplitItem>
          </Split>
        </PageSection>
        <PageSection>
          {skills.length === 0 ? (
            <EmptyState>
              <Title headingLevel="h2" size="lg">No skills yet</Title>
              <EmptyStateBody>Create or upload a skill to get started.</EmptyStateBody>
            </EmptyState>
          ) : (
            <Gallery hasGutter minWidths={{ default: '300px' }}>
              {skills.map(s => (
                <GalleryItem key={s.id}>
                  <Card isCompact>
                    <CardHeader
                      actions={{
                        actions: canEdit(s) ? (
                          <>
                            <Button variant="link" onClick={() => openEdit(s)}>Edit</Button>
                            <Button variant="link" isDanger onClick={() => handleDelete(s.id)}>Delete</Button>
                          </>
                        ) : undefined,
                      }}
                    >
                      <CardTitle>
                        <Split hasGutter>
                          <SplitItem isFilled>{s.name}</SplitItem>
                          <SplitItem>
                            <Switch
                              id={'toggle-' + s.id}
                              isChecked={s.enabled}
                              onChange={() => handleToggle(s)}
                              isDisabled={!canEdit(s)}
                              aria-label="Enable skill"
                            />
                          </SplitItem>
                        </Split>
                      </CardTitle>
                    </CardHeader>
                    <CardBody>
                      <p>{s.description}</p>
                      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '8px', alignItems: 'center', marginTop: '8px' }}>
                        <Label color={s.enabled ? 'green' : 'grey'}>
                          {s.enabled ? 'Enabled' : 'Disabled'}
                        </Label>
                        <Label color={s.is_global ? 'blue' : 'orange'}>
                          {s.is_global ? 'Global' : 'Private'}
                        </Label>
                        {s.owner && (
                          <Label color="grey">{s.owner}</Label>
                        )}
                        {canToggleGlobal(s) && (
                          <Switch
                            id={'global-' + s.id}
                            label="Share globally"
                            isChecked={s.is_global}
                            onChange={async () => {
                              await updateSkill(s.id, { is_global: !s.is_global } as any);
                              loadSkills();
                            }}
                            isReversed
                          />
                        )}
                      </div>
                    </CardBody>
                  </Card>
                </GalleryItem>
              ))}
            </Gallery>
          )}
        </PageSection>

      {/* Create modal */}
      <Modal
        variant={ModalVariant.medium}
        isOpen={showCreate}
        onClose={() => setShowCreate(false)}
      >
        <ModalHeader title="Create Skill" />
        <ModalBody>
          <FormGroup label="Name" isRequired fieldId="skill-name">
            <TextInput id="skill-name" value={name} onChange={(_e, v) => setName(v)} />
          </FormGroup>
          <FormGroup label="Description" isRequired fieldId="skill-desc">
            <TextInput id="skill-desc" value={description} onChange={(_e, v) => setDescription(v)} />
          </FormGroup>
          <FormGroup label="Content (Markdown)" isRequired fieldId="skill-content">
            <TextArea id="skill-content" value={content} onChange={(_e, v) => setContent(v)} rows={12} />
          </FormGroup>
        </ModalBody>
        <ModalFooter>
          <Button variant="primary" onClick={handleCreate} isDisabled={!name || !description || !content}>Create</Button>
          <Button variant="link" onClick={() => setShowCreate(false)}>Cancel</Button>
        </ModalFooter>
      </Modal>

      {/* Upload modal */}
      <Modal
        variant={ModalVariant.medium}
        isOpen={showUpload}
        onClose={() => setShowUpload(false)}
      >
        <ModalHeader title="Upload Skill File" />
        <ModalBody>
          <FormGroup label="Skill File (.md)" fieldId="skill-file">
            <FileUpload
              id="skill-file"
              value={uploadFile}
              filename={uploadFilename}
              onChange={handleFileChange}
              onFileInputChange={handleFileInputChange}
              dropzoneProps={{ accept: { 'text/markdown': ['.md'] } }}
            />
          </FormGroup>
          <FormGroup label="Name (optional)" fieldId="upload-name">
            <TextInput id="upload-name" value={name} onChange={(_e, v) => setName(v)} placeholder="Defaults to filename" />
          </FormGroup>
          <FormGroup label="Description (optional)" fieldId="upload-desc">
            <TextInput id="upload-desc" value={description} onChange={(_e, v) => setDescription(v)} />
          </FormGroup>
        </ModalBody>
        <ModalFooter>
          <Button variant="primary" onClick={handleUpload} isDisabled={!uploadFile}>Upload</Button>
          <Button variant="link" onClick={() => setShowUpload(false)}>Cancel</Button>
        </ModalFooter>
      </Modal>

      {/* Edit modal */}
      <Modal
        variant={ModalVariant.medium}
        isOpen={showEdit}
        onClose={() => setShowEdit(false)}
      >
        <ModalHeader title="Edit Skill" />
        <ModalBody>
          <FormGroup label="Name" isRequired fieldId="edit-name">
            <TextInput id="edit-name" value={name} onChange={(_e, v) => setName(v)} />
          </FormGroup>
          <FormGroup label="Description" isRequired fieldId="edit-desc">
            <TextInput id="edit-desc" value={description} onChange={(_e, v) => setDescription(v)} />
          </FormGroup>
          <FormGroup label="Content (Markdown)" isRequired fieldId="edit-content">
            <TextArea id="edit-content" value={content} onChange={(_e, v) => setContent(v)} rows={12} />
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
