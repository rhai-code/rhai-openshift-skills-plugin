# Openshift Skills Plugin

An OpenShift Console Plugin that manages SKILLS as Kubernetes Jobs.

  ![demo/skills-menu.png](demo/skills-menu.png)

## Multi-Tenancy & RBAC

The plugin uses OpenShift RBAC to control access. Two ClusterRoles are deployed with the Helm chart:

- **`skills-plugin-user`** — grants basic access
- **`skills-plugin-admin`** — grants full administrative access

Bind roles to users:

```bash
oc adm policy add-cluster-role-to-user skills-plugin-admin <admin-user>
oc adm policy add-cluster-role-to-user skills-plugin-user <regular-user>
```

### Permissions Summary

**Chat Sessions & Scheduled Tasks** (owner-scoped, no global sharing)

| Action | User | Admin |
|---|---|---|
| Create | Yes | Yes |
| View / Edit / Delete | Own only | All |

**Skills & MaaS Endpoints** (support global sharing)

| Action | User | Admin |
|---|---|---|
| Create | Yes (private by default) | Yes |
| View global | Yes (read-only) | Yes |
| View private | Own only | All |
| Edit / Delete | Own only | All |
| Share globally | Own only | All |

**Settings & Database**

| Action | User | Admin |
|---|---|---|
| View system prompt | Yes (read-only) | Yes |
| Edit system prompt | No | Yes |
| Export / Import database | No | Yes |

## Install

To install as a cluster-admin

```bash
helm upgrade --install skills-plugin chart/ -n skills-plugin --create-namespace
```

  ![demo/skills-console-plugin.png](demo/skills-console-plugin.png)

For MLFlow (disabled by default) support

```bash
COOKIE=$(openssl rand -base64 32)
helm upgrade --install skills-plugin chart/ -n skills-plugin --create-namespace --set mlflow.enabled=true --set mlflow.oauth.cookieSecret=$COOKIE
```

## Simple Demo

1. Deploy the helm chart

1. `Skills Settings` > Create a Model as a Service (MaaS) endpoint and test it works. This uses RHOAI3 MaaS - https://maas.apps.example.com/maas-api. Single model "/v1" endpoints are also supported if you don't have MaaS.

    ![demo/maas-endpoint.png](demo/maas-endpoint.png)

1. `Skills Manager` > Upload the [ETCD_SKILL.md](demo/ETCD_SKILL.md)

    ![demo/upload-skill.png](demo/upload-skill.png)

1. `Skills Schedule` > Schedule the skill to run. Create the rbac for the service account that will run your Job `oc apply -f demo/etcd-skill-rbac.yaml`. I have been lazy here and given it cluster-admin ! You will want to give it least privilege of course. Choose an image that has `oc` binary (and any other tools your skill may need). Set the `prompt` to run the parts of the skill you need.

    ![demo/schedule-skill.png](demo/schedule-skill.png)

1. Once the Job has run, the output can be seen in the `Skills Chat` as well as the scheduled skills screen. In this example we updated the prompt to defrag etcd based on the skill recommendation.

    ![demo/chat-output.png](demo/chat-output.png)

    ![demo/chat-output-1.png](demo/chat-output-1.png)

1. MLFlow (optional) support for tracing and observability.

    ![demo/mlflow-support.png](demo/mlflow-support.png)
