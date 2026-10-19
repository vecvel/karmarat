import Image from "next/image";
import Link from "next/link";

/**
 * Split sign-in layout: brand panel on the left with cartoon illustration and tagline, form on the right.
 */
export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="flex min-h-screen">
      {/* Brand panel */}
      <aside className="relative hidden w-[46%] flex-col justify-between bg-bark p-10 text-parchment lg:flex xl:w-[42%]">
        <Link href="/" className="flex items-center gap-3">
          <Image
            src="/logo.png"
            alt="Karmarat Logo"
            width={36}
            height={36}
            className="rounded-xs object-contain"
            priority
          />
          <span className="text-lg font-semibold tracking-tight text-parchment">Karmarat</span>
        </Link>

        <div className="my-auto flex flex-col items-center text-center">
          <div className="relative w-full max-w-sm mx-auto p-2">
            <Image
              src="/login-image.png"
              alt="Recruitment Illustration"
              width={500}
              height={480}
              priority
              className="h-auto aspect-square w-full object-contain"
            />
          </div>

          <div className="mt-4 max-w-sm">
            <h2 className="text-xl font-bold tracking-tight text-parchment">
              Smart & Effortless Recruitment
            </h2>
            <p className="mt-2 text-sm leading-relaxed text-parchment/75">
              Simplifying candidate pipelines, interviews, and hiring in one seamless place.
            </p>
          </div>
        </div>

        <div className="flex items-center justify-center gap-4 text-[11px] text-parchment/50">
          <span>Version 1.0.0</span>
          <span className="size-1 rounded-full bg-parchment/30" />
          <span>My Organisation</span>
        </div>
      </aside>

      {/* Form panel */}
      <main className="flex flex-1 flex-col items-center justify-center px-5 py-10 sm:px-8">
        <div className="w-full max-w-100">
          <Link
            href="/"
            className="mb-8 flex items-center gap-2.5 lg:hidden"
            aria-label="Karmarat home"
          >
            <Image
              src="/logo.png"
              alt="Karmarat Logo"
              width={32}
              height={32}
              className="rounded-xs object-contain"
              priority
            />
            <span className="font-semibold tracking-tight">Karmarat</span>
          </Link>
          {children}
        </div>
      </main>
    </div>
  );
}
