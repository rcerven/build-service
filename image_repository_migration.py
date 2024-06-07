#!/usr/bin/env python
import base64
import copy
import json
import subprocess
import os
import yaml
from urllib.error import HTTPError
from urllib.parse import urlencode, quote_plus
from urllib.request import Request, urlopen

IMAGE_REPOSITORY_BASE = {
    'apiVersion': 'appstudio.redhat.com/v1alpha1',
    'kind': 'ImageRepository',
    'metadata': {
        'annotations': {
            'image-controller.appstudio.redhat.com/update-component-image': 'true'
        },
        'labels': {
            'appstudio.redhat.com/application': None,
            'appstudio.redhat.com/component': None,
        },
        'name': None,
        'namespace': None,
    },
    'spec': {
        'image': {
            'name': None,
            'visibility': 'public',
        }
    }
}

IMAGE_ANNOTATION = 'image.redhat.com/image'
QUAY_API_URL = "https://quay.io/api/v1"
JSON_FILE = './imagerepository.json'

TOKEN =
NAMESPACE = 'redhat-user-workloads-stage'

TOKEN =
NAMESPACE = 'redhat-user-workloads'

def run(cmd):
    print("Subprocess: %s" % ' '.join(cmd))
    p = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    stdout, stderr = p.communicate()

    return p.returncode, stdout, stderr

def save_json(data, filename):
    with open(filename, 'w') as fp:
        json.dump(data, fp, indent=2)

def get_robot_account_from_secret(secret_json, comp_image_annotation):
    config_json_str = secret_json.get('data', {}).get('.dockerconfigjson')
    if not config_json_str:
        return None

    config_json = json.loads(base64.b64decode(config_json_str))
    final_config_json = copy.deepcopy(config_json)
    if len(config_json['auths']) != 1:
        if not comp_image_annotation:
            print(f"ERROR: secret has more entries: {secret_json['metadata']['name']}, and image annotation is empty")
            exit(1)

        print(f"secret has more entries will find auth based on repo, has entries: {len(config_json['auths'])}")
        image_json = json.loads(comp_image_annotation)
        image = image_json.get('image')

        for auth_key in config_json['auths'].keys():
            if auth_key != image:
                del final_config_json['auths'][auth_key]

        if len(final_config_json['auths']) != 1:
            print(f"ERROR: secret {secret_json['metadata']['name']} doesn't have only 1 auth: {config_json['auths']}")
            exit(1)

    for auth in final_config_json['auths'].values():
        robot_account, _ = base64.b64decode(auth['auth']).decode().split(':')

    if '+' not in robot_account:
        print(f"!!! robot account id different format : {robot_account}")
        return None

    _, account_name = robot_account.split('+', 1)
    return account_name

def robot_account_exists(robot_account):
    api_url = f"{QUAY_API_URL}/organization/{NAMESPACE}/robots/{quote_plus(robot_account)}"
    print(f"trying to get robot account: {api_url}")
    request = Request(api_url, headers={
        "Authorization": f"Bearer {TOKEN}",
    })

    try:
        with urlopen(request) as resp:
            print(f"robot account response status: {resp.status}")
            if resp.status != 200:
                raise RuntimeError(resp.reason)
            return True

    except HTTPError as ex:
        print(f"robot account existance check: {ex.status}")
        if ex.status != 400 and ex.status != 404:
            raise RuntimeError(f"ERROR: HTTPError : {ex}")
    return False

def remove_robot_account(robot_account):
    api_url = f"{QUAY_API_URL}/organization/{NAMESPACE}/robots/{quote_plus(robot_account)}"
    print(api_url)
    request = Request(api_url, method="DELETE", headers={
        "Authorization": f"Bearer {TOKEN}",
    })

    while True:
        try:
            with urlopen(request) as resp:
                if resp.status != 200 and resp.status != 204 and resp.status != 404:
                    raise RuntimeError(resp.reason)
                else:
                    break
        except HTTPError as ex:
            print(f"HTTPError exception: {ex}, will retry")

def parse_components(all_components):
    get_secrets = ['oc', 'get', '-A', 'secrets', '-o', 'json']
    retcode, output, error = run(get_secrets)
    if retcode != 0:
        print('ERROR: Failed to get secrets: %s', error)
        print('You should login to openshift')
        exit(1)
    all_secrets = json.loads(output)

    get_ir = ['oc', 'get', '-A', 'imagerepository', '-o', 'json']
    retcode, output, error = run(get_ir)
    if retcode != 0:
        print('ERROR: Failed to get imagerepository: %s', error)
        print('You should login to openshift')
        exit(1)
    all_ir = json.loads(output)

    for config in all_components['items']:
        comp_name = config['metadata']['name']
        comp_namespace = config['metadata']['namespace']
        comp_image_annotation = config['metadata'].get('annotations', {}).get(IMAGE_ANNOTATION)
        comp_containerimage = config['spec'].get('containerImage')
        application = config['spec']['application']

        get_sa = ['oc', 'get', '-n', comp_namespace, 'serviceaccount', 'appstudio-pipeline', '-o', 'json']
        retcode, output, error = run(get_sa)
        if retcode != 0:
            print('ERROR: Failed to get serviceaccount: %s', error)
            print('You should login to openshift')
            exit(1)
        serviceaccount = json.loads(output)

        print(f"Processing {comp_name} - {comp_namespace}")
        print(f"application: {application}")
        print(f"image annotation: {comp_image_annotation}")
        print(f"containerimage: {comp_containerimage}")
        print("================")

        old_pull_secret = f"{comp_name}-pull"
        old_pull_secret_found = False
        old_push_secret = comp_name
        old_push_secret_found = False
        new_pull_secret = f"imagerepository-for-{application}-{comp_name}-image-pull"
        new_pull_secret_found = False
        new_push_secret = f"imagerepository-for-{application}-{comp_name}-image-push"
        new_push_secret_found = False
        image_repository_name = f"imagerepository-for-{application}-{comp_name}"
        image_repository_found = False

        pull_secret_json = {}
        push_secret_json = {}

        for secret in all_secrets['items']:

            if secret['metadata']['name'] == old_pull_secret and secret['metadata']['namespace'] == comp_namespace:
                old_pull_secret_found = True
                pull_secret_json = secret
                continue

            if secret['metadata']['name'] == old_push_secret and secret['metadata']['namespace'] == comp_namespace:
                old_push_secret_found = True
                push_secret_json = secret
                continue

            if secret['metadata']['name'] == new_pull_secret and secret['metadata']['namespace'] == comp_namespace:
                new_pull_secret_found = True
                continue

            if secret['metadata']['name'] == new_push_secret and secret['metadata']['namespace'] == comp_namespace:
                new_push_secret_found = True
                continue

        image_repository_json = {}
        for imagerepository in all_ir['items']:
            if imagerepository['metadata']['name'] == image_repository_name and imagerepository['metadata']['namespace'] == comp_namespace:
                image_repository_found = True
                image_repository_json = imagerepository
                break

        print(f"old_pull_secret: {old_pull_secret} - {old_pull_secret_found}")
        print(f"old_push_secret: {old_push_secret} - {old_push_secret_found}")
        print(f"new_pull_secret: {new_pull_secret} - {new_pull_secret_found}")
        print(f"new_push_secret: {new_push_secret} - {new_push_secret_found}")
        print(f"imagerepository: {image_repository_name} - {image_repository_found}")
        print("================")

        if not image_repository_found:
            print(f"have to create image repository: {image_repository_name}")
            if not comp_image_annotation:
                print(f"!!!------- image annotation doesn't exist, skipping component")
                continue

            ir_dict = copy.deepcopy(IMAGE_REPOSITORY_BASE)
            ir_dict['metadata']['labels']['appstudio.redhat.com/application'] = application
            ir_dict['metadata']['labels']['appstudio.redhat.com/component'] = comp_name
            ir_dict['metadata']['name'] = image_repository_name
            ir_dict['metadata']['namespace'] = comp_namespace

            if comp_image_annotation:
                del ir_dict['metadata']['annotations']

            image_json = json.loads(comp_image_annotation)
            image = image_json.get('image')
            visibility = image_json.get('visibility', 'public')

            if not image:
                print(f"!!!----------------- image annotation is missing image : {image_json}, skipping component")
                continue

            image_part = image.split('/')[3:]
            image_final = '/'.join(image_part)
            if not image_final:
                print(f"!!!----------------- image in annotation has wrong format : {image}")
                continue

            ir_dict['spec']['image']['name'] = image_final
            ir_dict['spec']['image']['visibility'] = visibility

            print(ir_dict)

            save_json(ir_dict, JSON_FILE)
            create_imagerepository = ['oc', 'create', '-f', JSON_FILE]
            retcode, output, error = run(create_imagerepository)
            if retcode == 0:
                print(output)
            else:
                print(f"Failed to create imagerepository {image_repository_name} in {comp_namespace} : {error}")
                exit(1)

        elif not comp_containerimage:
            print("have to add containerImage to component")
            patch = '{"spec": {"containerImage": "%s"}}' % image_repository_json['status']['image']['url']
            patch_component = ['oc', 'patch', '-n', comp_namespace, f"component/{comp_name}", '-p', patch, '--type', 'merge']
            print(patch_component)

            retcode, output, error = run(patch_component)
            if retcode == 0:
                print(output)
            else:
                print(f"Failed to update component {comp_name} in {comp_namespace} : {error}")
                exit(1)

        if old_pull_secret_found:
            # remove robot account
            robot_account = get_robot_account_from_secret(pull_secret_json, comp_image_annotation)
            print(f"robot account in secret: {robot_account}")

            if robot_account is not None:
                account_exists = robot_account_exists(robot_account)
                print(f"pull robot account found: {account_exists}")

                if account_exists:
                    print(f"removing pull robot account : {robot_account}")
                    remove_robot_account(robot_account)
            else:
                print("robot account not found in the secret")

            # remove secret
            print(f"have to remove old secret: {old_pull_secret}")
            delete_secret = ['oc', 'delete', '-n', comp_namespace, 'secret', old_pull_secret]
            print(delete_secret)

            retcode, output, error = run(delete_secret)
            if retcode == 0:
                print(output)
            else:
                print(f"Failed to delete secret: {old_pull_secret}")
                exit(1)


        if old_push_secret_found:
            # remove robot account
            robot_account = get_robot_account_from_secret(push_secret_json, comp_image_annotation)
            print(f"robot account in secret: {robot_account}")

            if robot_account is not None:
                account_exists = robot_account_exists(robot_account)
                print(f"push robot account found: {account_exists}")

                if account_exists:
                    print(f"removing push robot account : {robot_account}")
                    remove_robot_account(robot_account)
            else:
                print("robot account not found in the secret")

            print('=======================')
            print(serviceaccount['secrets'])
            print(serviceaccount['imagePullSecrets'])

            secret_index = None
            secret_pull_index = None
            for i in range(len(serviceaccount['secrets'])):
                if serviceaccount['secrets'][i]['name'] == old_push_secret:
                    secret_index = i
                    break
            for i in range(len(serviceaccount['imagePullSecrets'])):
                if serviceaccount['imagePullSecrets'][i]['name'] == old_push_secret:
                    secret_pull_index = i
                    break

            # unlink secrets from SA
            if secret_index is not None:
                print(f"have to unlink old secret: {old_push_secret} from SA 'secrets'")
                patch = '[{"op": "remove", "path": "/secrets/%s"}]' % secret_index
                unlink_secret = ['oc', 'patch', '-n', comp_namespace, 'serviceaccount/appstudio-pipeline', '-p', patch, '--type', 'json']
                print(unlink_secret)

                retcode, output, error = run(unlink_secret)
                if retcode == 0:
                    print(output)
                else:
                    print(f"Failed to update service account : {error}")
                    exit(1)

            if secret_pull_index is not None:
                print(f"have to unlink old secret: {old_push_secret} from SA 'imagePullSecrets'")
                patch = '[{"op": "remove", "path": "/imagePullSecrets/%s"}]' % secret_pull_index
                unlink_secret = ['oc', 'patch', '-n', comp_namespace, 'serviceaccount/appstudio-pipeline', '-p', patch, '--type', 'json']
                print(unlink_secret)

                retcode, output, error = run(unlink_secret)
                if retcode == 0:
                    print(output)
                else:
                    print(f"Failed to update service account : {error}")
                    exit(1)

            # remove secret
            print(f"have to remove old secret: {old_push_secret}")
            delete_secret = ['oc', 'delete', '-n', comp_namespace, 'secret', old_push_secret]
            print(delete_secret)

            retcode, output, error = run(delete_secret)
            if retcode == 0:
                print(output)
            else:
                print(f"Failed to delete secret: {old_push_secret}")
                exit(1)

            print('=======================')


        if comp_image_annotation:
            print(f"have to remove component annotation: {IMAGE_ANNOTATION}")
            patch = '[{"op": "remove", "path": "/metadata/annotations/image.redhat.com~1image"}]'
            patch_component = ['oc', 'patch', '-n', comp_namespace, f"component/{comp_name}",  '-p', patch, '--type', 'json']
            print(patch_component)

            retcode, output, error = run(patch_component)
            if retcode == 0:
                print(output)
            else:
                print(f"Failed to update component {comp_name} in {comp_namespace} : {error}")
                exit(1)

         if 'labels' in serviceaccount['metadata']:
             if 'appstudio.redhat.com/linked-by-remote-secret' in serviceaccount['metadata']['labels']:
                print("have to remove linked-by-remote-secret label from SA")
                patch = '[{"op": "remove", "path": "/metadata/labels/appstudio.redhat.com~1linked-by-remote-secret"}]'
                delete_label = ['oc', 'patch', '-n', comp_namespace, 'serviceaccount/appstudio-pipeline', '-p', patch, '--type', 'json']
                print(delete_label)

                retcode, output, error = run(delete_label)
                if retcode == 0:
                    print(output)
                else:
                    print(f"Failed to update service account : {error}")
                    exit(1)

        if 'annotations' in serviceaccount['metadata']:
            if 'appstudio.redhat.com/linked-remote-secrets' in serviceaccount['metadata']['annotations']:
                print("have to remove appstudio.redhat.com/linked-remote-secrets annotation from SA")
                patch = '[{"op": "remove", "path": "/metadata/annotations/appstudio.redhat.com~1linked-remote-secrets"}]'
                delete_ann = ['oc', 'patch', '-n', comp_namespace, 'serviceaccount/appstudio-pipeline', '-p', patch, '--type', 'json']
                print(delete_ann)

                retcode, output, error = run(delete_ann)
                if retcode == 0:
                    print(output)
                else:
                    print(f"Failed to update service account : {error}")
                    exit(1)

        print("===========================================================================================================================================================================")


def main():
    get_components = ['oc', 'get', '-A', 'components', '-o', 'json']
    retcode, output, error = run(get_components)

    if retcode != 0:
        print('ERROR: Failed to get components: %s', error)
        print('You should login to openshift')
        exit(1)

    all_components = json.loads(output)

    parse_components(all_components)

if __name__ == '__main__':
    raise SystemExit(main())
