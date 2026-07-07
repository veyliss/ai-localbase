import { RefObject, useEffect, useId, useRef } from 'react'

type InertElement = HTMLElement & { inert?: boolean }

interface InertSnapshot {
  element: InertElement
  ariaHidden: string | null
  inertAttribute: string | null
  inert: boolean
}

interface ModalFocusTrapOptions {
  enabled?: boolean
  inertSiblings?: boolean
  initialFocusRef?: RefObject<HTMLElement>
  onClose?: () => void
  restoreFocus?: boolean
}

const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

const modalStack: string[] = []

const getFocusableElements = (root: HTMLElement) =>
  Array.from(root.querySelectorAll<HTMLElement>(focusableSelector)).filter((element) => {
    const style = window.getComputedStyle(element)
    return style.visibility !== 'hidden' && style.display !== 'none' && element.getClientRects().length > 0
  })

const focusElement = (element: HTMLElement | null | undefined) => {
  element?.focus({ preventScroll: true })
}

export const useModalFocusTrap = <T extends HTMLElement>(
  modalRef: RefObject<T>,
  {
    enabled = true,
    inertSiblings = true,
    initialFocusRef,
    onClose,
    restoreFocus = true,
  }: ModalFocusTrapOptions = {},
) => {
  const modalId = useId()
  const previousActiveElementRef = useRef<Element | null>(null)

  useEffect(() => {
    const modalElement = modalRef.current
    if (!enabled || !modalElement) return

    previousActiveElementRef.current = document.activeElement
    modalStack.push(modalId)

    const inertSnapshots: InertSnapshot[] = []
    if (inertSiblings && modalElement.parentElement) {
      Array.from(modalElement.parentElement.children).forEach((sibling) => {
        if (sibling === modalElement || !(sibling instanceof HTMLElement)) return

        const element = sibling as InertElement
        inertSnapshots.push({
          element,
          ariaHidden: element.getAttribute('aria-hidden'),
          inertAttribute: element.getAttribute('inert'),
          inert: element.inert,
        })
        element.setAttribute('aria-hidden', 'true')
        element.setAttribute('inert', '')
        element.inert = true
      })
    }

    const isTopModal = () => modalStack[modalStack.length - 1] === modalId

    const focusFirstAvailable = () => {
      if (!isTopModal()) return
      focusElement(initialFocusRef?.current ?? getFocusableElements(modalElement)[0] ?? modalElement)
    }

    const animationFrame = window.requestAnimationFrame(focusFirstAvailable)

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isTopModal()) return

      if (event.key === 'Escape' && onClose) {
        event.preventDefault()
        event.stopPropagation()
        onClose()
        return
      }

      if (event.key !== 'Tab') return

      const focusableElements = getFocusableElements(modalElement)
      if (focusableElements.length === 0) {
        event.preventDefault()
        focusElement(modalElement)
        return
      }

      const firstElement = focusableElements[0]
      const lastElement = focusableElements[focusableElements.length - 1]
      const activeElement = document.activeElement

      if (event.shiftKey && activeElement === firstElement) {
        event.preventDefault()
        focusElement(lastElement)
      } else if (!event.shiftKey && activeElement === lastElement) {
        event.preventDefault()
        focusElement(firstElement)
      } else if (!modalElement.contains(activeElement)) {
        event.preventDefault()
        focusElement(event.shiftKey ? lastElement : firstElement)
      }
    }

    const handleFocusIn = (event: FocusEvent) => {
      if (!isTopModal()) return
      if (event.target instanceof Node && modalElement.contains(event.target)) return
      focusFirstAvailable()
    }

    document.addEventListener('keydown', handleKeyDown, true)
    document.addEventListener('focusin', handleFocusIn, true)

    return () => {
      window.cancelAnimationFrame(animationFrame)
      document.removeEventListener('keydown', handleKeyDown, true)
      document.removeEventListener('focusin', handleFocusIn, true)

      const stackIndex = modalStack.lastIndexOf(modalId)
      if (stackIndex >= 0) {
        modalStack.splice(stackIndex, 1)
      }

      inertSnapshots.forEach(({ element, ariaHidden, inertAttribute, inert }) => {
        if (ariaHidden === null) {
          element.removeAttribute('aria-hidden')
        } else {
          element.setAttribute('aria-hidden', ariaHidden)
        }
        if (inertAttribute === null) {
          element.removeAttribute('inert')
        } else {
          element.setAttribute('inert', inertAttribute)
        }
        element.inert = inert
      })

      if (restoreFocus && previousActiveElementRef.current instanceof HTMLElement) {
        focusElement(previousActiveElementRef.current)
      }
    }
  }, [enabled, inertSiblings, initialFocusRef, modalId, modalRef, onClose, restoreFocus])
}
