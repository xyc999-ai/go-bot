import whale from './assets/harness-whale.png'

export function HarnessAvatar({ className }: { className: string }) {
  return <img className={className} src={whale} alt="" width={384} height={384} />
}
