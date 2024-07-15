#!/usr/bin/env python
import copy
import json
import subprocess
import os

notification_prod = {
    "config": {
        "url": "https://bombino.api.redhat.com/v1/sbom/quay/push"
    },
    "event": "repo_push",
    "method": "webhook",
    "title": "SBOM-event-to-Bombino"
}

notification_stage = {
    "config": {
        "url": "https://bombino.preprod.api.redhat.com/v1/sbom/quay/push"
    },
    "event": "repo_push",
    "method": "webhook",
    "title": "SBOM-event-to-Bombino"
}

CLUSTER_TYPE = "stage"
CLUSTER_TYPE = "prod"

JSON_FILE = './imagerepository.json'

def run(cmd):
    print("Subprocess: %s" % ' '.join(cmd))
    p = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    stdout, stderr = p.communicate()

    return p.returncode, stdout, stderr

def save_json(data, filename):
    with open(filename, 'w') as fp:
        json.dump(data, fp, indent=2)

def parse_ir(all_ir):
    if CLUSTER_TYPE == "prod":
        notification_add = copy.deepcopy(notification_prod)
    elif CLUSTER_TYPE == "stage":
        notification_add = copy.deepcopy(notification_stage)
    else:
        print(f'unknown cluster selected {CLUSTER_TYPE}')
        exit(1)

    for config in all_ir['items']:
        ir_name = config['metadata']['name']
        ir_namespace = config['metadata']['namespace']
        notifications = config['spec'].get('notifications')

        print("================================================================================================================================")

        print(f"Processing {ir_name} - {ir_namespace}")

        print("================notifications")
        print(notifications)
        print("================")

        should_update = False
        if not notifications:
            print("notifications are empty, will add notification")
            config['spec']['notifications'] = [notification_add]
            print(config['spec']['notifications'])
            should_update = True

        else:
            if notification_add in notifications:
                print("notifications already have the notificaion")
            else:
                print("notifications aren't empty, but don't have the notificaion, will add it")
                config['spec']['notifications'].append(notification_add)
                print(config['spec']['notifications'])
                should_update = True

        if should_update:
            save_json(config, JSON_FILE)
            print(f"updating imagerepository with new notification {ir_name}")
            update_imagerepository = ['oc', 'replace', '-f', JSON_FILE]
            retcode, output, error = run(update_imagerepository)
            if retcode == 0:
                print(output)
            else:
                print(f"Failed to update imagerepository {ir_name} in {ir_namespace} : {error}")
                exit(1)


def main():
    get_ir = ['oc', 'get', '-A', 'imagerepository', '-o', 'json']
    retcode, output, error = run(get_ir)

    if retcode != 0:
        print('ERROR: Failed to get imagerepositories: %s', error)
        print('You should login to openshift')
        exit(1)

    all_ir = json.loads(output)

    parse_ir(all_ir)

if __name__ == '__main__':
    raise SystemExit(main())
