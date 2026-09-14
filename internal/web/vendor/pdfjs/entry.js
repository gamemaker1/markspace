// Entry point for the vendored pdf.js bundle. Re-exports only what
// static/js/viewers/pdf.js uses. Rebuild with ./build.sh after changing it.
export {
  getDocument,
  GlobalWorkerOptions,
  TextLayer,
  version,
  AbortException,
  InvalidPDFException,
  PasswordException,
  RenderingCancelledException,
} from "pdfjs-dist/build/pdf.mjs";
