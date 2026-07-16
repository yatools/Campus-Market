import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import BaseModal from './BaseModal.vue'

describe('BaseModal', () => {
  it('closes only from the backdrop or close button', async () => {
    const wrapper = mount(BaseModal, {
      props: { title: 'Test modal' },
      slots: { default: '<p>Body</p>' },
      global: { stubs: { teleport: true } },
    })

    await wrapper.get('.modal-card').trigger('click')
    expect(wrapper.emitted('close')).toBeUndefined()

    await wrapper.get('.modal-backdrop').trigger('click')
    await wrapper.get('.icon-button').trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(2)
  })
})
