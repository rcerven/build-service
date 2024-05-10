#!/usr/bin/env python
import json
import subprocess
import os
import yaml

PIPELINE_ANNOTATION = "build.appstudio.openshift.io/pipeline"

PIPELINES_DICT = {'fbc-builder': 'latest',
                  'docker-build': 'latest'}

def run(cmd):
    print("Subprocess: %s" % ' '.join(cmd))
    p = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    stdout, stderr = p.communicate()

    return p.returncode, stdout, stderr

def update_components(all_components):
    for config in all_components['items']:
        print("Processing %s - %s" % (config['metadata']['name'], config['metadata']['namespace']))
        pipeline_annotation = None

        if 'annotations' not in config['metadata']:
            print("     missing annotations")

        elif PIPELINE_ANNOTATION not in config['metadata']['annotations']:
            print("     missing pipeline annotations")

        elif not config['metadata']['annotations'][PIPELINE_ANNOTATION]:
            print("     pipeline annotations is empty: %s" % config['metadata']['annotations'][PIPELINE_ANNOTATION])

        else:
            pipeline_annotation = json.loads(config['metadata']['annotations'][PIPELINE_ANNOTATION])

        builder = 'docker-build'

        if 'status' in config and 'devfile' in config['status']:
            devfile = config['status']['devfile']

            devfile_yaml = yaml.safe_load(devfile)

            if 'language' in devfile_yaml['metadata'] and devfile_yaml['metadata']['language'] == 'fbc':
                builder = 'fbc-builder'
                print('     has fbc language')


        print('     builder: %s' % builder)

        if pipeline_annotation:
            print(f"  pipeline annotation : {pipeline_annotation}")

            if builder != pipeline_annotation['name']:
                print(f"have to update pipeline, has: {pipeline_annotation['name']} and should be {builder}")
            else:
                continue

        annotation = '{"metadata": {"annotations":{"%s":"{\\"name\\":\\"%s\\",\\"bundle\\":\\"%s\\"}"}}}' % (PIPELINE_ANNOTATION, builder, PIPELINES_DICT[builder])
        patch_component = ['oc', 'patch', '-n', config['metadata']['namespace'], f"component/{config['metadata']['name']}", '-p', annotation, '--type', 'merge']

        retcode, output, error = run(patch_component)
        if retcode == 0:
            print(output)
        else:
            print("ERROR: Failed to update component %s - %s : %s" % (config['metadata']['name'], config['metadata']['namespace'], error))
            exit(1)

def main():
    get_image_streams = ['oc', 'get', '-A', 'components', '-o', 'json']
    retcode, output, error = run(get_image_streams)

    if retcode != 0:
        print('ERROR: Failed to get components: %s', error)
        print('You should login to openshift')
        exit(1)

    all_components = json.loads(output)

    update_components(all_components)

if __name__ == '__main__':
    raise SystemExit(main())
