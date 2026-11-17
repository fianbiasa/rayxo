import { Form, Head, Link } from '@inertiajs/react';
import { Button } from '@/components/ui/button';
import { Spinner } from '@/components/ui/spinner';
import { dashboard } from '@/routes';

export default function LoginLink({ token }: { token: string }) {
    return (
        <>
            <Head title="Buka akun hosting" />

            <Form action={`/login-link/${token}`} method="post">
                {({ processing }) => (
                    <div className="flex flex-col gap-6">
                        <p className="text-sm text-muted-foreground">
                            Kamu sedang masuk. Melanjutkan link ini akan
                            mengeluarkanmu dari sesi ini dan membuka akun
                            hosting yang dituju link tersebut.
                        </p>

                        <Button
                            className="h-11 w-full rounded-xl rayxo-brand-gradient text-[15px] font-semibold shadow-brand"
                            disabled={processing}
                            data-test="login-link-confirm-button"
                        >
                            {processing && <Spinner />}
                            Lanjutkan dan buka akun
                        </Button>

                        <Link
                            href={dashboard()}
                            className="text-center text-sm text-muted-foreground hover:underline"
                        >
                            Batal, tetap di sesi ini
                        </Link>
                    </div>
                )}
            </Form>
        </>
    );
}

LoginLink.layout = {
    title: 'Buka akun hosting',
    description:
        'Konfirmasi sebelum berpindah ke akun hosting dari link masuk ini.',
};
