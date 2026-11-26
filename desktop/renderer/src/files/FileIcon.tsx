import { FileArchive, FileAudio, FileCode, FileImage, FileText, FileVideo } from 'lucide-react'
import icons from '../assets/file-icons/vscode-icons.json'
const formats: Record<string, keyof typeof icons> = {
  pdf: 'pdf', doc: 'word', docx: 'word', odt: 'word', rtf: 'word',
  xls: 'excel', xlsx: 'excel', csv: 'excel', ods: 'excel',
  ppt: 'powerpoint', pptx: 'powerpoint', odp: 'powerpoint', md: 'markdown', markdown: 'markdown',
}
export function FileIcon({ name }: { name: string }) {
  const extension = name.split('.').at(-1)?.toLowerCase() ?? ''
  const svg = icons[formats[extension]]
  const Icon = /^(zip|tar|gz|rar|7z)$/.test(extension) ? FileArchive : /^(png|jpg|jpeg|gif|webp|svg)$/.test(extension) ? FileImage
    : /^(mp3|wav|m4a|flac)$/.test(extension) ? FileAudio : /^(mp4|mov|webm)$/.test(extension) ? FileVideo
      : /^(js|ts|json|html|css|py|go|sh|yaml)$/.test(extension) ? FileCode : FileText
  return <span className="file-type-icon" aria-hidden="true">{svg ? <img src={'data:image/svg+xml,' + encodeURIComponent(svg)} alt="" /> : <Icon size={22} strokeWidth={1.5} />}</span>
}
