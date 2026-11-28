const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('desktop', {
  platform: process.platform,
  oauth: { open: (id, value) => ipcRenderer.invoke('desktop:oauth-open', id, value), cancel: id => ipcRenderer.invoke('desktop:oauth-cancel', id) },
  setLanguage: value => ipcRenderer.invoke('desktop:language', value),
  configuration: () => ipcRenderer.invoke('desktop:configuration'),
  notify: value => ipcRenderer.invoke('desktop:notify', value),
  onCommand: listener => {
    const handler = (_event, value) => listener(value)
    ipcRenderer.on('desktop:command', handler)
    return () => ipcRenderer.removeListener('desktop:command', handler)
  },
})
