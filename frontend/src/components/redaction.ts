import { Mark } from '@tiptap/core'

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    redaction: {
      toggleRedaction: () => ReturnType
    }
  }
}

export const Redaction = Mark.create({
  name: 'redaction',
  inclusive: false,

  parseHTML() {
    return [{ tag: 'span[data-redaction]' }]
  },

  renderHTML() {
    return ['span', { class: 'redaction-mark', 'data-redaction': 'true' }, 0]
  },

  markdownTokenizer: {
    name: 'redaction',
    level: 'inline',
    start: (source) => source.indexOf('=='),
    tokenize: (source, _tokens, lexer) => {
      const match = /^==([^=\r\n]+)==/.exec(source)
      if (!match) return undefined
      return {
        type: 'redaction',
        raw: match[0],
        text: match[1],
        tokens: lexer.inlineTokens(match[1]),
      }
    },
  },

  parseMarkdown: (token, helpers) =>
    helpers.applyMark('redaction', helpers.parseInline(token.tokens || [])),

  renderMarkdown: (node, helpers) =>
    `==${helpers.renderChildren(node.content || [])}==`,

  addCommands() {
    return {
      toggleRedaction: () => ({ commands }) => commands.toggleMark(this.name),
    }
  },
})

export function maskRedactedMarkdown(value: string): string {
  return value.replace(/==([^=\r\n]+)==/g, '▓▓▓▓▓▓')
}
