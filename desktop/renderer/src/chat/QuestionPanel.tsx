import { useState } from 'react'
import { useLocale } from '../locale'
import { answerQuestion } from '../api/controller'
import { MarkdownText } from './ChatContent'
import type { Answer, PendingQuestion } from './transcript'

export function QuestionPanel({ item, userId, sessionId }: { item: PendingQuestion; userId: string; sessionId: string }) {
  const { t } = useLocale()
  const [answers, setAnswers] = useState<Answer[]>(() => item.questions.map(question => ({ id: question.id, selected: [] })))
  const [sending, setSending] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [error, setError] = useState('')
  const review = item.questions.some(question => question.id === 'plan-review')
  const ready = answers.every(answer => answer.selected.length > 0 || Boolean(answer.custom?.trim()))

  function update(index: number, patch: Partial<Answer>) {
    setAnswers(previous => previous.map((answer, position) => position === index ? { ...answer, ...patch } : answer))
  }

  async function submit() {
    if (!ready || sending || submitted) return
    setSending(true)
    setError('')
    try {
      await answerQuestion(userId, sessionId, item.callId, answers)
      setSubmitted(true)
    } catch {
      setError('The answer could not be sent. Try again.')
    } finally {
      setSending(false)
    }
  }

  return <form className={review ? "question-panel plan-review" : "question-panel"} aria-label={t(review ? 'Plan review' : 'Agent question')} onSubmit={event => { event.preventDefault(); void submit() }}>
    <strong>{review ? t('Review the plan') : item.state === 'continued' ? t('You can still answer') : t('The agent has a question')}</strong>
    {item.questions.map((question, index) => <fieldset key={question.id} disabled={sending || submitted}>
      <legend>{t(question.question)}</legend>
      {question.detail && <div className="question-detail markdown"><MarkdownText text={question.detail} /></div>}
      {question.options?.map(option => <label className="question-option" key={option.label}>
        <input type={question.multiSelect ? 'checkbox' : 'radio'} name={question.id}
          checked={answers[index].selected.includes(option.label)}
          onChange={event => {
            const selected = answers[index].selected
            update(index, { selected: question.multiSelect
              ? event.target.checked ? [...selected, option.label] : selected.filter(label => label !== option.label)
              : [option.label], ...(!question.multiSelect && { custom: undefined }) })
          }} />
        <span>{t(option.label)}{option.description && <small>{t(option.description)}</small>}</span>
      </label>)}
      <input className="question-custom" type="text" value={answers[index].custom ?? ''}
        aria-label={t('Custom answer for {question}', { question: t(question.question) })} placeholder={t('Type your own answer')}
        onChange={event => update(index, { custom: event.target.value, ...(!question.multiSelect && { selected: [] }) })} />
    </fieldset>)}
    {error && <p className="feedback" role="alert">{t(error)}</p>}
    <button className="text-button" type="submit" disabled={!ready || sending || submitted}>{submitted ? t('Answer sent') : t('Send answer')}</button>
  </form>
}
