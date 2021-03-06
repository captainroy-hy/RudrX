patch: {
	spec: template: spec: {
		// +patchKey=name
		containers: [{
			name: context.name
			// +patchKey=name
			volumeMounts: [{
				name:      parameter.mountName
				mountPath: parameter.appMountPath
			}]
		}]
		initContainers: [{
			name:    parameter.name
			image:   parameter.image
			command: parameter.command
			// +patchKey=name
			volumeMounts: [{
				name:      parameter.mountName
				mountPath: parameter.initMountPath
			}]
		}]
		// +patchKey=name
		volumes: [{
			name: parameter.mountName
			emptyDir: {}
		}]
	}
}
parameter: {
	name:  string
	image: string
	command?: [...string]
	mountName:     *"workdir" | string
	appMountPath:  string
	initMountPath: string
}
